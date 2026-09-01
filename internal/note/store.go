package note

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store { return &Store{path: path} }
func (s *Store) Path() string     { return s.path }

func (s *Store) EnsurePath() error {
	return os.MkdirAll(filepath.Dir(s.path), 0o755)
}

func (s *Store) Load() ([]Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Note{}, nil
		}
		return nil, fmt.Errorf("读取便笺失败: %w", err)
	}
	if len(b) == 0 {
		return []Note{}, nil
	}
	var doc struct {
		Version int    `json:"version"`
		Notes   []Note `json:"notes"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		_ = os.WriteFile(s.path+".bak", b, 0o600)
		return nil, fmt.Errorf("解析便笺数据失败（原文件已备份到 .bak）: %w", err)
	}
	if doc.Version != 0 && doc.Version != 1 {
		return nil, fmt.Errorf("不支持的便笺数据版本: %d", doc.Version)
	}
	if doc.Notes == nil {
		doc.Notes = []Note{}
	}
	return doc.Notes, nil
}

func (s *Store) Save(items []Note) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, err := os.ReadFile(s.path); err == nil && len(existing) > 0 {
		var header struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(existing, &header); err != nil {
			return fmt.Errorf("现有便笺数据损坏，拒绝覆盖: %w", err)
		}
		if header.Version > 1 {
			return fmt.Errorf("现有便笺数据版本 %d 过新，拒绝覆盖", header.Version)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("检查现有便笺数据失败: %w", err)
	}
	if items == nil {
		items = []Note{}
	}
	doc := struct {
		Version int    `json:"version"`
		Notes   []Note `json:"notes"`
	}{Version: 1, Notes: items}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化便笺失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建便笺目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".notes-*.tmp")
	if err != nil {
		return fmt.Errorf("创建便笺临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入便笺临时文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("同步便笺临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭便笺临时文件失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("收紧便笺文件权限失败: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("原子替换便笺文件失败: %w", err)
	}
	return nil
}
