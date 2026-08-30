package reminder

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// OnFireFunc 触发回调。参数是 reminder 的快照 + 触发的"理想时间"。
// 回调里不要做长阻塞操作（建议扔到 goroutine），避免阻塞调度器主循环。
type OnFireFunc func(r Reminder, at time.Time)

// fireCatchUpWindow timer 到点后允许的最大迟到时间。
// timer 正常抖动在秒级；超过该窗口（如电脑长时间休眠刚醒）则不补触发，
// 避免开机被一堆积压提醒轰炸。
const fireCatchUpWindow = 2 * time.Minute

// Manager 提醒的总线：内存状态 + 调度器 + 触发回调。
//
// 线程模型：
//   - 所有公开方法（List/Get/Add/Update/Delete/Toggle/FireNow）线程安全。
//   - 调度器 timer 跑在 Manager 内部的 goroutine 里，触发时持锁改状态 + 调 OnFire。
type Manager struct {
	store  *Store
	onFire OnFireFunc
	now    func() time.Time // 可注入，便于测试

	mu    sync.Mutex
	items map[string]*Reminder // id → reminder（指针便于原地改 Enabled / LastFiredAt / FiredCount）
	timer *time.Timer          // 当前正在等的那一发；Cancel 后 Rebuild 才有效

	// pauseUntil：触发器在该时刻之前整体静默（不弹窗）。用于"暂停今日"场景。
	// 设为 time.Time{}（零值）表示无暂停。每次 fire 之前检查，PauseUntil(time.Time{}) 解除暂停。
	pauseUntil time.Time

	persistMu sync.Mutex // 序列化 persist 调用，避免并发快照互相覆盖
}

// NewManager 构造 Manager。Load 现存提醒 + Rebuild 调度。
func NewManager(store *Store, onFire OnFireFunc) (*Manager, error) {
	m := &Manager{
		store:  store,
		onFire: onFire,
		now:    time.Now,
		items:  make(map[string]*Reminder),
	}
	loaded, err := store.Load()
	if err != nil {
		// 损坏 JSON 已被 store 备份 + 返回空，不阻断启动
		fmt.Fprintln(os.Stderr, "WARN: 加载 reminders 失败:", err)
	}
	for i := range loaded {
		r := loaded[i]
		m.items[r.ID] = &r
	}
	m.Rebuild()
	return m, nil
}

// NewID 生成新 ID（16 字节随机 hex）。
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 极低概率，回退时间戳
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// SetOnFire 替换触发回调（main.go 初始化晚于 NewManager 时用）。
func (m *Manager) SetOnFire(f OnFireFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onFire = f
}

// PauseUntil 把所有提醒静默到 t 时刻为止。t 为零值表示解除暂停。
// fire() 会检查 PauseUntil 自动跳过，调用方无需手动 Rebuild。
func (m *Manager) PauseUntil(t time.Time) {
	m.mu.Lock()
	m.pauseUntil = t
	m.mu.Unlock()
}

// PauseStatus 返回当前暂停状态。
//
// 返回值：
//   - paused: 是否正在暂停
//   - until:  暂停结束时间（RFC3339），未暂停时为空字符串
func (m *Manager) PauseStatus() (paused bool, until string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pauseUntil.IsZero() || m.pauseUntil.Before(m.now()) {
		return false, ""
	}
	return true, m.pauseUntil.Format(time.RFC3339)
}

// List 返回全部提醒（按下次触发时间升序；过期排最后）。
func (m *Manager) List() []Reminder {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	out := make([]Reminder, 0, len(m.items))
	for _, r := range m.items {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		ni := out[i].NextFire(now)
		nj := out[j].NextFire(now)
		zeroI := ni.IsZero()
		zeroJ := nj.IsZero()
		if zeroI != zeroJ {
			return !zeroI // 非零排前面
		}
		if zeroI && zeroJ {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return ni.Before(nj)
	})
	return out
}

// Get 按 ID 取一条。
func (m *Manager) Get(id string) (Reminder, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.items[id]
	if !ok {
		return Reminder{}, false
	}
	return *r, true
}

// Add 新增。返回新分配的 reminder（含 ID / CreatedAt / UpdatedAt）。
func (m *Manager) Add(in Reminder) (Reminder, error) {
	if err := in.Validate(); err != nil {
		return Reminder{}, err
	}
	now := m.now()
	in.ID = NewID()
	in.CreatedAt = now.Format(time.RFC3339)
	in.UpdatedAt = in.CreatedAt
	in.LastFiredAt = ""
	in.FiredCount = 0

	m.mu.Lock()
	m.items[in.ID] = &in
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		// 回滚内存状态
		m.mu.Lock()
		delete(m.items, in.ID)
		m.mu.Unlock()
		return Reminder{}, err
	}
	m.Rebuild()
	return in, nil
}

// Update 全量替换。ID / CreatedAt 不变；UpdatedAt 自动更新；Enabled 保留原值。
func (m *Manager) Update(id string, in Reminder) (Reminder, error) {
	m.mu.Lock()
	old, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return Reminder{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if in.Type == "" {
		in.Type = old.Type
	}
	if strings.TrimSpace(in.Content) == "" {
		in.Content = old.Content
	}
	if in.Type == TypeOnce && strings.TrimSpace(in.At) == "" {
		in.At = old.At
	}
	if (in.Type == TypeWeekly || in.Type == TypeMonthly) && strings.TrimSpace(in.Time) == "" {
		in.Time = old.Time
	}
	if in.Type == TypeWeekly && len(in.Weekdays) == 0 {
		in.Weekdays = old.Weekdays
	}
	if in.Type == TypeMonthly && in.DayOfMonth == 0 {
		in.DayOfMonth = old.DayOfMonth
	}
	if in.Type == TypeCron && strings.TrimSpace(in.Cron) == "" {
		in.Cron = old.Cron
	}
	// 注意：LeadMinutes 不做零值合并 —— 0 是合法值（"不提前"），
	// 前端 PUT 时总是显式携带，省略即重置为 0（与其他字段语义一致）。
	if in.Action == nil {
		in.Action = old.Action
	}
	if strings.TrimSpace(in.SourceNoteID) == "" {
		in.SourceNoteID = old.SourceNoteID
	}
	m.mu.Unlock()
	if err := in.Validate(); err != nil {
		return Reminder{}, err
	}
	m.mu.Lock()
	old2, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return Reminder{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	in.ID = id
	in.CreatedAt = old2.CreatedAt
	in.UpdatedAt = m.now().Format(time.RFC3339)
	in.LastFiredAt = old2.LastFiredAt
	in.FiredCount = old2.FiredCount
	in.Enabled = old2.Enabled
	m.items[id] = &in
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		m.mu.Lock()
		m.items[id] = old2
		m.mu.Unlock()
		return Reminder{}, err
	}
	m.Rebuild()
	return in, nil
}

// Delete 删除。
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	old, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(m.items, id)
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		// 回滚
		m.mu.Lock()
		m.items[id] = old
		m.mu.Unlock()
		return err
	}
	m.Rebuild()
	return nil
}

// Toggle 切换 enabled。
func (m *Manager) Toggle(id string) (Reminder, error) {
	m.mu.Lock()
	r, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return Reminder{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	r.Enabled = !r.Enabled
	r.UpdatedAt = m.now().Format(time.RFC3339)
	out := *r
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		// 回滚
		m.mu.Lock()
		r.Enabled = !r.Enabled
		m.mu.Unlock()
		return Reminder{}, err
	}
	m.Rebuild()
	return out, nil
}

// FireNow 立即触发（前端"测试"按钮用）。不影响 last_fired_at 与下次调度。
func (m *Manager) FireNow(id string) error {
	m.mu.Lock()
	r, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	snap := *r
	m.mu.Unlock()
	if m.onFire != nil {
		go func() {
			defer func() {
				if rv := recover(); rv != nil {
					log.Printf("reminder: onFire panic: %v", rv)
				}
			}()
			m.onFire(snap, m.now())
		}()
	}
	return nil
}

// Rebuild 重新计算下次触发时间 + 重置 timer。
//
// 在 Add / Update / Delete / Toggle / NewManager 后调用。
// 取消当前 timer，算所有 enabled 项的下次触发，取最近，挂新 timer。
func (m *Manager) Rebuild() {
	m.mu.Lock()
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	now := m.now()
	var nextAt time.Time
	var nextID string
	var nextType Type
	for _, r := range m.items {
		if !r.Enabled {
			continue
		}
		t := r.NextFire(now)
		if t.IsZero() {
			continue
		}
		if nextAt.IsZero() || t.Before(nextAt) {
			nextAt = t
			nextID = r.ID
			nextType = r.Type
		}
	}
	if nextAt.IsZero() {
		log.Printf("reminder.Rebuild: 无待触发提醒（now=%s）", now.Format("2006-01-02 15:04:05"))
		m.mu.Unlock()
		return
	}
	d := nextAt.Sub(now)
	m.timer = time.AfterFunc(d, m.fire)
	log.Printf("reminder.Rebuild: 挂上 timer id=%s type=%s 下次触发=%s 距今=%s",
		nextID, nextType, nextAt.Format("2006-01-02 15:04:05"), d.Truncate(time.Second))
	m.mu.Unlock()
}

// fire timer 到点。串行：先锁内改 LastFiredAt + 算下一次，单次提醒 disabled，
// 然后调 onFire，最后 Rebuild 挂下一次。
func (m *Manager) fire() {
	m.mu.Lock()
	now := m.now()
	log.Printf("reminder.fire: timer 到点 now=%s items=%d", now.Format("2006-01-02 15:04:05.000"), len(m.items))

	// 全局暂停检查：到点但 pauseUntil 还在生效（> now），跳过本次触发。
	if !m.pauseUntil.IsZero() && m.pauseUntil.After(now) {
		log.Printf("reminder.fire: 全局暂停中 pauseUntil=%s，跳过并重排", m.pauseUntil.Format("2006-01-02 15:04:05"))
		m.mu.Unlock()
		m.Rebuild()
		return
	}

	var due []Reminder
	for _, r := range m.items {
		if !r.Enabled {
			continue
		}
		// 用 DueAt(now) 拿"刚刚到达"的槽（<= now 的最近一次）。
		// 注意：不能用 NextFire(now)——它只返回严格未来时间，timer 到点时
		// NextFire 会返回下一个周期，导致所有提醒都被判"未到点"而永远不触发。
		t := r.DueAt(now)
		if t.IsZero() {
			log.Printf("reminder.fire: 跳过 id=%s type=%s DueAt=零值（未到点或无效）", r.ID, r.Type)
			continue
		}
		// 容差窗口：timer 正常抖动几秒；超过窗口（如电脑休眠很久刚醒）不补触发，
		// 避免开机被一堆积压提醒轰炸。
		diff := now.Sub(t)
		if diff < 0 || diff > fireCatchUpWindow {
			log.Printf("reminder.fire: 跳过 id=%s type=%s due=%s diff=%s 超出容差窗口(%s)",
				r.ID, r.Type, t.Format("2006-01-02 15:04:05"), diff.Truncate(time.Second), fireCatchUpWindow)
			continue
		}
		// 防重复触发：该槽已触发过（LastFiredAt >= 槽时间）则跳过。
		// 典型场景：同一天 fire() 因别的提醒被再次调用，本周期的槽不应重复弹窗。
		if r.LastFiredAt != "" {
			if last, err := time.ParseInLocation(time.RFC3339, r.LastFiredAt, time.Local); err == nil && !last.Before(t) {
				log.Printf("reminder.fire: 跳过 id=%s type=%s 已触发过 last=%s >= due=%s",
					r.ID, r.Type, last.Format("2006-01-02 15:04:05"), t.Format("2006-01-02 15:04:05"))
				continue
			}
		}
		log.Printf("reminder.fire: 命中 id=%s type=%s due=%s 准备弹窗", r.ID, r.Type, t.Format("2006-01-02 15:04:05"))
		r.LastFiredAt = now.Format(time.RFC3339)
		r.FiredCount++
		if r.Type == TypeOnce {
			r.Enabled = false
		}
		due = append(due, *r)
	}
	m.mu.Unlock()

	if len(due) > 0 {
		if err := m.persist(); err != nil {
			log.Printf("reminder: persist 失败: %v", err)
		}
		// 触发回调（异步，不阻塞）
		if m.onFire != nil {
			for _, r := range due {
				go func(r Reminder) {
					defer func() {
						if rv := recover(); rv != nil {
							log.Printf("reminder: onFire panic: %v", rv)
						}
					}()
					m.onFire(r, now)
				}(r)
			}
		}
	}
	m.Rebuild()
}

// persist 写盘（持锁的内部方法）。
func (m *Manager) persist() error {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()

	snap := make([]Reminder, 0, len(m.items))
	m.mu.Lock()
	for _, r := range m.items {
		snap = append(snap, *r)
	}
	m.mu.Unlock()
	return m.store.Save(snap)
}

// Stop 停止调度（退出时用）。
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
}

// ErrNotFound 提醒不存在。
var ErrNotFound = errors.New("提醒不存在")
