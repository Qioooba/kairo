package note

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	store *Store
	now   func() time.Time

	mu      sync.RWMutex
	items   map[string]Note
	subs    map[uint64]chan Event
	nextSub uint64
}

func NewManager(store *Store) (*Manager, error) {
	items, err := store.Load()
	if err != nil {
		return nil, err
	}
	m := &Manager{
		store: store,
		now:   time.Now,
		items: make(map[string]Note, len(items)),
		subs:  make(map[uint64]chan Event),
	}
	for i := range items {
		n := items[i]
		n.Normalize()
		if n.ID == "" || n.Validate() != nil {
			continue
		}
		if n.Revision == 0 {
			n.Revision = 1
		}
		m.items[n.ID] = clone(n)
	}
	return m, nil
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("note-%d", time.Now().UnixNano())
}

func clone(n Note) Note {
	if n.Desktop != nil {
		d := *n.Desktop
		n.Desktop = &d
	}
	return n
}

func (m *Manager) snapshotLocked() []Note {
	out := make([]Note, 0, len(m.items))
	for _, n := range m.items {
		out = append(out, clone(n))
	}
	return out
}

func (m *Manager) List(f Filter) []Note {
	m.mu.RLock()
	defer m.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(f.Query))
	out := make([]Note, 0, len(m.items))
	for _, n := range m.items {
		if f.Archived != nil && n.Archived != *f.Archived {
			continue
		}
		if f.Floating != nil && n.Floating != *f.Floating {
			continue
		}
		if f.DesktopOnly && (n.Desktop == nil || !n.Desktop.Visible) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(n.Title+"\n"+n.Body), q) {
			continue
		}
		out = append(out, clone(n))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *Manager) Get(id string) (Note, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n, ok := m.items[id]
	return clone(n), ok
}

func (m *Manager) Add(in Note) (Note, error) {
	in.Normalize()
	if err := in.Validate(); err != nil {
		return Note{}, err
	}
	m.mu.Lock()
	if len(m.items) >= MaxNotes {
		m.mu.Unlock()
		return Note{}, fmt.Errorf("便笺数量已达到上限 %d", MaxNotes)
	}
	if in.DesktopVisible() && m.desktopVisibleLocked("") >= MaxDesktopVisible {
		m.mu.Unlock()
		return Note{}, ErrDesktopLimit
	}
	now := m.now()
	in.ID = newID()
	in.Revision = 1
	in.CreatedAt = now
	in.UpdatedAt = now
	previousFloating := map[string]Note{}
	if in.Floating {
		for id, other := range m.items {
			if !other.Floating {
				continue
			}
			previousFloating[id] = clone(other)
			other.Floating = false
			other.Revision++
			other.UpdatedAt = now
			m.items[id] = other
		}
	}
	m.items[in.ID] = clone(in)
	if err := m.store.Save(m.snapshotLocked()); err != nil {
		delete(m.items, in.ID)
		for id, other := range previousFloating {
			m.items[id] = other
		}
		m.mu.Unlock()
		return Note{}, err
	}
	m.mu.Unlock()
	for id := range previousFloating {
		if other, ok := m.Get(id); ok {
			m.broadcast(Event{Kind: "updated", Note: ptrClone(other), ID: id})
		}
	}
	m.broadcast(Event{Kind: "created", Note: ptrClone(in), ID: in.ID})
	return clone(in), nil
}

func (m *Manager) Patch(id string, p Patch) (Note, error) {
	m.mu.Lock()
	old, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return Note{}, ErrNotFound
	}
	if p.BaseRevision == 0 || p.BaseRevision != old.Revision {
		cur := clone(old)
		m.mu.Unlock()
		return Note{}, &ConflictError{Current: cur}
	}
	n := clone(old)
	if p.Title != nil {
		n.Title = *p.Title
	}
	if p.Body != nil {
		n.Body = *p.Body
	}
	if p.Color != nil {
		n.Color = *p.Color
	}
	if p.Pinned != nil {
		n.Pinned = *p.Pinned
	}
	if p.Floating != nil {
		n.Floating = *p.Floating
	}
	if p.Archived != nil {
		n.Archived = *p.Archived
	}
	if p.Desktop != nil {
		if *p.Desktop == nil {
			n.Desktop = nil
		} else {
			d := **p.Desktop
			n.Desktop = &d
		}
	}
	n.Normalize()
	if err := n.Validate(); err != nil {
		m.mu.Unlock()
		return Note{}, err
	}
	if n.DesktopVisible() && !old.DesktopVisible() && m.desktopVisibleLocked(id) >= MaxDesktopVisible {
		m.mu.Unlock()
		return Note{}, ErrDesktopLimit
	}
	n.Revision++
	n.UpdatedAt = m.now()
	previousFloating := map[string]Note{}
	if n.Floating && !old.Floating {
		for otherID, other := range m.items {
			if otherID != id && other.Floating {
				previousFloating[otherID] = clone(other)
				other.Floating = false
				other.Revision++
				other.UpdatedAt = n.UpdatedAt
				m.items[otherID] = other
			}
		}
	}
	m.items[id] = clone(n)
	if err := m.store.Save(m.snapshotLocked()); err != nil {
		m.items[id] = old
		for otherID, other := range previousFloating {
			m.items[otherID] = other
		}
		m.mu.Unlock()
		return Note{}, err
	}
	m.mu.Unlock()
	for otherID := range previousFloating {
		if other, ok := m.Get(otherID); ok {
			m.broadcast(Event{Kind: "updated", Note: ptrClone(other), ID: otherID})
		}
	}
	m.broadcast(Event{Kind: "updated", Note: ptrClone(n), ID: id})
	return clone(n), nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	old, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	delete(m.items, id)
	if err := m.store.Save(m.snapshotLocked()); err != nil {
		m.items[id] = old
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	m.broadcast(Event{Kind: "deleted", ID: id})
	return nil
}

func (m *Manager) desktopVisibleLocked(except string) int {
	n := 0
	for id, item := range m.items {
		if id == except {
			continue
		}
		if item.DesktopVisible() {
			n++
		}
	}
	return n
}

func ptrClone(n Note) *Note {
	c := clone(n)
	return &c
}

// Subscribe returns a buffered event channel and an idempotent unsubscribe.
// The channel is intentionally not closed on unsubscribe, avoiding a close/send
// race with a broadcast snapshot.
func (m *Manager) Subscribe() (<-chan Event, func()) {
	m.mu.Lock()
	m.nextSub++
	id := m.nextSub
	ch := make(chan Event, 32)
	m.subs[id] = ch
	m.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.subs, id)
			m.mu.Unlock()
		})
	}
}

func (m *Manager) broadcast(ev Event) {
	m.mu.RLock()
	chs := make([]chan Event, 0, len(m.subs))
	for _, ch := range m.subs {
		chs = append(chs, ch)
	}
	m.mu.RUnlock()
	for _, ch := range chs {
		select {
		case ch <- ev:
		default:
		}
	}
}
