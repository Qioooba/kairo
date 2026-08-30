package dbconsole

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type storeFile struct {
	Version int      `json:"version"`
	Sources []Source `json:"sources"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data storeFile
}

func NewStore(dataDir string) (*Store, error) {
	s := &Store{path: filepath.Join(dataDir, "database-sources.json"), data: storeFile{Version: 1}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取数据源配置失败: %w", err)
	}
	var data storeFile
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("解析数据源配置失败: %w", err)
	}
	if data.Version != 1 {
		return fmt.Errorf("不支持的数据源配置版本 %d", data.Version)
	}
	seen := make(map[string]struct{}, len(data.Sources))
	for i := range data.Sources {
		data.Sources[i].Defaults()
		if err := data.Sources[i].Validate(); err != nil {
			return fmt.Errorf("数据源 %q 非法: %w", data.Sources[i].Name, err)
		}
		if _, ok := seen[data.Sources[i].ID]; ok {
			return fmt.Errorf("数据源 id %q 重复", data.Sources[i].ID)
		}
		seen[data.Sources[i].ID] = struct{}{}
	}
	s.data = data
	return nil
}

func (s *Store) List() []Source {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]Source(nil), s.data.Sources...)
	for i := range out {
		out[i].AllowedUsers = append([]string(nil), out[i].AllowedUsers...)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (s *Store) Get(id string) (Source, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, source := range s.data.Sources {
		if source.ID == id {
			source.AllowedUsers = append([]string(nil), source.AllowedUsers...)
			return source, true
		}
	}
	return Source{}, false
}

func (s *Store) Save(source Source) (Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	if source.ID == "" {
		id, err := randomID()
		if err != nil {
			return Source{}, err
		}
		source.ID = id
		source.CreatedAt = now
	}
	source.UpdatedAt = now
	source.Defaults()
	if err := source.Validate(); err != nil {
		return Source{}, err
	}
	for _, item := range s.data.Sources {
		if item.ID != source.ID && strings.EqualFold(item.Name, source.Name) {
			return Source{}, fmt.Errorf("数据源名称 %q 已存在", source.Name)
		}
	}
	next := storeFile{Version: s.data.Version, Sources: append([]Source(nil), s.data.Sources...)}
	found := false
	for i := range next.Sources {
		if next.Sources[i].ID == source.ID {
			if source.CreatedAt == "" {
				source.CreatedAt = next.Sources[i].CreatedAt
			}
			next.Sources[i] = source
			found = true
			break
		}
	}
	if !found {
		if len(next.Sources) >= 100 {
			return Source{}, errors.New("数据源数量不能超过 100")
		}
		next.Sources = append(next.Sources, source)
	}
	if err := s.writeLocked(next); err != nil {
		return Source{}, err
	}
	s.data = next
	return source, nil
}

func (s *Store) Delete(id string) (Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, source := range s.data.Sources {
		if source.ID != id {
			continue
		}
		next := storeFile{Version: s.data.Version, Sources: make([]Source, 0, len(s.data.Sources)-1)}
		next.Sources = append(next.Sources, s.data.Sources[:i]...)
		next.Sources = append(next.Sources, s.data.Sources[i+1:]...)
		if err := s.writeLocked(next); err != nil {
			return Source{}, err
		}
		s.data = next
		return source, nil
	}
	return Source{}, fs.ErrNotExist
}

func (s *Store) writeLocked(data storeFile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".database-sources-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	ok = true
	return nil
}

func randomID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "db_" + hex.EncodeToString(b), nil
}
