// Package preferences persists user-scoped workbench preferences.
//
// Deployment configuration belongs in config.yaml, credentials belong in the
// credential store, and domain data belongs in its own store. This package is
// deliberately limited to small, non-secret UI/workbench state.
package preferences

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"
)

const Version = 1

var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type document struct {
	Version int                                   `json:"version"`
	Global  map[string]any                        `json:"global"`
	Users   map[string]map[string]json.RawMessage `json:"users"`
}

type Store struct {
	mu   sync.Mutex
	path string
}

func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) Global() (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	return cloneMap(doc.Global)
}

func (s *Store) ReplaceGlobal(value map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	cloned, err := cloneMap(value)
	if err != nil {
		return err
	}
	doc.Global = cloned
	return s.writeLocked(doc)
}

func (s *Store) Get(user, namespace string) (json.RawMessage, bool, error) {
	if err := validateKey(user, namespace); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return nil, false, err
	}
	value, ok := doc.Users[user][namespace]
	return append(json.RawMessage(nil), value...), ok, nil
}

func (s *Store) Put(user, namespace string, value json.RawMessage) error {
	if err := validateKey(user, namespace); err != nil {
		return err
	}
	if len(value) == 0 || !json.Valid(value) {
		return errors.New("偏好内容必须是有效 JSON")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	if doc.Users[user] == nil {
		doc.Users[user] = make(map[string]json.RawMessage)
	}
	doc.Users[user][namespace] = append(json.RawMessage(nil), value...)
	return s.writeLocked(doc)
}

func (s *Store) Delete(user, namespace string) error {
	if err := validateKey(user, namespace); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	if scopes := doc.Users[user]; scopes != nil {
		delete(scopes, namespace)
		if len(scopes) == 0 {
			delete(doc.Users, user)
		}
	}
	return s.writeLocked(doc)
}

func validateKey(user, namespace string) error {
	if user == "" || len(user) > 256 {
		return errors.New("偏好用户标识非法")
	}
	if !namespacePattern.MatchString(namespace) {
		return errors.New("偏好模块名非法")
	}
	return nil
}

func emptyDocument() document {
	return document{Version: Version, Global: map[string]any{}, Users: map[string]map[string]json.RawMessage{}}
}

func (s *Store) loadLocked() (document, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) || len(raw) == 0 {
		return emptyDocument(), nil
	}
	if err != nil {
		return document{}, fmt.Errorf("读取偏好失败: %w", err)
	}
	var doc document
	if err := json.Unmarshal(raw, &doc); err == nil && doc.Version == Version && doc.Global != nil && doc.Users != nil {
		return doc, nil
	}

	// v0 legacy format was the global preference object itself. Preserve it as
	// the global section and convert on the next successful write.
	var legacy map[string]any
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return document{}, fmt.Errorf("解析偏好失败: %w", err)
	}
	if _, hasVersion := legacy["version"]; hasVersion {
		return document{}, errors.New("不支持的偏好文件版本")
	}
	doc = emptyDocument()
	doc.Global = legacy
	return doc, nil
}

func (s *Store) writeLocked(doc document) error {
	doc.Version = Version
	if doc.Global == nil {
		doc.Global = map[string]any{}
	}
	if doc.Users == nil {
		doc.Users = map[string]map[string]json.RawMessage{}
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化偏好失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建偏好目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".preferences-*.tmp")
	if err != nil {
		return fmt.Errorf("创建偏好临时文件失败: %w", err)
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
		return fmt.Errorf("收紧偏好文件权限失败: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入偏好失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("同步偏好失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭偏好文件失败: %w", err)
	}
	if err := replaceFile(tmpName, s.path); err != nil {
		return fmt.Errorf("原子替换偏好失败: %w", err)
	}
	ok = true
	return nil
}

func replaceFile(src, dst string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := os.Rename(src, dst); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if runtime.GOOS != "windows" {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	return lastErr
}

func cloneMap(in map[string]any) (map[string]any, error) {
	if in == nil {
		return map[string]any{}, nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
