package schedtask

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const dataVersion = 1

// Store 任务定义 + 运行历史的持久化层。
//
// 文件格式（两个 JSON 文件同目录）：
//
//	sched_tasks.json:      {"version":1, "tasks":[{Task},...]}
//	sched_task_runs.json:  {"version":1, "runs":{taskID:[{RunRecord},...]}}
//
// 写策略：先写临时文件 + os.Rename 原子替换（与 reminder.Store 一致）。
// 读策略：文件不存在 → 视为空；JSON 损坏 → 备份到 .bak + 返回空。
type Store struct {
	tasksPath string
	runsPath  string
	mu        sync.Mutex // 串行化所有磁盘 IO，避免并发写坏文件
}

func NewStore(tasksPath, runsPath string) *Store {
	return &Store{tasksPath: tasksPath, runsPath: runsPath}
}

// TasksPath 返回任务定义文件路径。
func (s *Store) TasksPath() string { return s.tasksPath }

// RunsPath 返回运行历史文件路径。
func (s *Store) RunsPath() string { return s.runsPath }

// LoadTasks 读全部任务。失败自动备份坏文件 + 返回空列表。
func (s *Store) LoadTasks() ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.tasksPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Task{}, nil
		}
		return nil, fmt.Errorf("读 %s 失败: %w", s.tasksPath, err)
	}
	if len(data) == 0 {
		return []Task{}, nil
	}
	var doc struct {
		Version int    `json:"version"`
		Tasks   []Task `json:"tasks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		_ = backupLocked(s.tasksPath, data)
		return []Task{}, fmt.Errorf("解析 %s 失败（已备份到 .bak）: %w", s.tasksPath, err)
	}
	if doc.Version < 0 || doc.Version > dataVersion {
		return nil, fmt.Errorf("不支持的定时任务数据版本: %d（当前支持 %d）", doc.Version, dataVersion)
	}
	if doc.Tasks == nil {
		return []Task{}, nil
	}
	return doc.Tasks, nil
}

// SaveTasks 写全部任务（原子替换）。
func (s *Store) SaveTasks(items []Task) error {
	if items == nil {
		items = []Task{}
	}
	doc := struct {
		Version int    `json:"version"`
		Tasks   []Task `json:"tasks"`
	}{Version: dataVersion, Tasks: items}
	return s.saveJSON(s.tasksPath, ".sched-tasks-*.tmp", doc)
}

// LoadRuns 读全部运行历史（taskID → 记录列表，按时间升序）。
func (s *Store) LoadRuns() (map[string][]RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := map[string][]RunRecord{}
	data, err := os.ReadFile(s.runsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("读 %s 失败: %w", s.runsPath, err)
	}
	if len(data) == 0 {
		return out, nil
	}
	var doc struct {
		Version int                    `json:"version"`
		Runs    map[string][]RunRecord `json:"runs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		_ = backupLocked(s.runsPath, data)
		return out, fmt.Errorf("解析 %s 失败（已备份到 .bak）: %w", s.runsPath, err)
	}
	if doc.Version < 0 || doc.Version > dataVersion {
		return nil, fmt.Errorf("不支持的任务运行历史版本: %d（当前支持 %d）", doc.Version, dataVersion)
	}
	if doc.Runs != nil {
		out = doc.Runs
	}
	return out, nil
}

// SaveRuns 写全部运行历史（原子替换）。
func (s *Store) SaveRuns(runs map[string][]RunRecord) error {
	if runs == nil {
		runs = map[string][]RunRecord{}
	}
	doc := struct {
		Version int                    `json:"version"`
		Runs    map[string][]RunRecord `json:"runs"`
	}{Version: dataVersion, Runs: runs}
	return s.saveJSON(s.runsPath, ".sched-runs-*.tmp", doc)
}

// saveJSON 序列化 + 原子落盘（临时文件 + rename）。
func (s *Store) saveJSON(path, tmpPattern string, doc any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		var header struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(existing, &header); err != nil {
			return fmt.Errorf("现有任务数据损坏，拒绝覆盖: %w", err)
		}
		if header.Version > dataVersion {
			return fmt.Errorf("现有任务数据版本 %d 过新，拒绝覆盖", header.Version)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("检查现有任务数据失败: %w", err)
	}

	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化失败: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, tmpPattern)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("写临时文件失败: %w", err)
	}
	// 先把内容刷到磁盘，再做替换；避免系统异常退出后目标文件已经换名，
	// 但数据仍只停留在缓存中。
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("同步临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("收紧权限失败: %w", err)
	}
	if err := replaceFile(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("原子替换失败: %w", err)
	}
	return nil
}

// backupLocked 把坏 JSON 备份到 .bak。
func backupLocked(path string, data []byte) error {
	return os.WriteFile(path+".bak", data, 0o600)
}

// EnsurePath 确保数据目录存在（首次启动用）。
func (s *Store) EnsurePath() error {
	dir := filepath.Dir(s.tasksPath)
	if dir == "" {
		return errors.New("路径为空")
	}
	return os.MkdirAll(dir, 0o755)
}
