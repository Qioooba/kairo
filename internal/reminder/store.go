package reminder

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store reminders.json 持久化层。
//
// 文件格式：
//
//	{
//	  "version": 1,
//	  "reminders": [ {Reminder}, ... ]
//	}
//
// 写策略：先写临时文件 + os.Rename 原子替换（preferences 模式一致）。
// 读策略：文件不存在 → 视为空；JSON 损坏 → 备份到 reminders.json.bak + 返回空。
type Store struct {
	path string
	mu   sync.Mutex // 串行化所有磁盘 IO，避免并发写坏文件
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path 返回数据文件路径。
func (s *Store) Path() string { return s.path }

// Load 读全部提醒。失败自动备份坏文件 + 返回空列表。
func (s *Store) Load() ([]Reminder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Reminder{}, nil
		}
		return nil, fmt.Errorf("读 %s 失败: %w", s.path, err)
	}
	if len(data) == 0 {
		return []Reminder{}, nil
	}

	var doc struct {
		Version   int        `json:"version"`
		Reminders []Reminder `json:"reminders"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		// JSON 损坏：备份原文件 + 返回空（不阻断启动）
		_ = s.backupLocked(data)
		return []Reminder{}, fmt.Errorf("解析 %s 失败（已备份到 .bak）: %w", s.path, err)
	}
	if doc.Reminders == nil {
		return []Reminder{}, nil
	}
	return doc.Reminders, nil
}

// Save 写全部提醒（原子替换）。
func (s *Store) Save(items []Reminder) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if items == nil {
		items = []Reminder{}
	}
	doc := struct {
		Version   int        `json:"version"`
		Reminders []Reminder `json:"reminders"`
	}{Version: 1, Reminders: items}

	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化失败: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".reminders-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("写临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("收紧权限失败: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("原子替换失败: %w", err)
	}
	return nil
}

// backupLocked 把坏 JSON 备份到 .bak（已持锁）。
func (s *Store) backupLocked(data []byte) error {
	bak := s.path + ".bak"
	return os.WriteFile(bak, data, 0o600)
}

// EnsurePath 确保数据目录存在（首次启动用）。
func (s *Store) EnsurePath() error {
	dir := filepath.Dir(s.path)
	if dir == "" {
		return errors.New("路径为空")
	}
	return os.MkdirAll(dir, 0o755)
}