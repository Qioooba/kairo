// Package browserpref 持久化"上次成功打开工具箱的浏览器"。
//
// 为什么需要这个：Windows 下有 Chrome / Edge / 360 / 安全浏览器并存，
// 用户上次用什么浏览器打开，工具箱下次就用同一个 —— 比每次重新探测稳。
//
// 数据模型：data/browser_state.json
//
//	{
//	  "kind": "chrome",          // "chrome" / "default"
//	  "path": "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
//	  "updated_at": "2026-07-07T22:51:00+08:00"
//	}
//
// kind 设计：标记"上次用的是 Chrome"还是"系统默认浏览器"。
//   - kind=chrome + path 失效 → 回探测链（用户可能卸了 Chrome）
//   - kind=default + 用户之后装 Chrome → 不会主动换（尊重上次选择）
//   - 首次启动（state 不存在）→ 探测链把 Chrome 优先，命中即用
//
// 为什么不用 config.yaml：上次浏览器是"运行时状态"而非"用户配置"，
// 不该污染用户的 config diff；写透明放到独立文件更干净（和 credentials.json 同套路）。
//
// 写策略：atomic（写到 .tmp + os.Rename），Windows 上 rename 偶发被杀毒实时扫描
// 挡住，参照 credentials.saveFile 做 100ms 重试一次。
package browserpref

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Kind 区分"上次打开用的是 Chrome 还是走系统默认"。
type Kind string

const (
	KindChrome  Kind = "chrome"  // Chrome.exe（系统装了 Chrome，走专用路径）
	KindDefault Kind = "default" // 系统默认浏览器（rundll32/open/xdg-open）
)

// State 持久化的浏览器偏好。
//
// Path 在 kind=chrome 时是 chrome.exe 的绝对路径；
// kind=default 时为空字符串（rundll32 之类不需要路径）。
type State struct {
	Kind      Kind     `json:"kind"`
	Path      string   `json:"path,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// fileName JSON 文件名（落在 dataDir 下面，跟 credentials/credkey 同级）。
const fileName = "browser_state.json"

// fileMode state 文件权限（仅当前用户可读写，跟凭据文件同一档）。
const fileMode = 0o600

var (
	dataDirMu sync.RWMutex
	dataDir   string // 由 Init 设置；为空表示还没初始化
)

// Init 初始化 dataDir。
//
// 必须在 Read/Write/Reset 之前调用一次；多次调用是幂等的（最后一次生效）。
//
// dataDir 通常是 cfg.DataDir()。
func Init(d string) {
	dataDirMu.Lock()
	defer dataDirMu.Unlock()
	dataDir = d
}

// Path 返回 state 文件的绝对路径。
//
// 必须先调 Init，否则返回空字符串 + error。
func Path() (string, error) {
	dataDirMu.RLock()
	dir := dataDir
	dataDirMu.RUnlock()
	if dir == "" {
		return "", errors.New("browserpref: 未初始化（请先调用 Init）")
	}
	return filepath.Join(dir, fileName), nil
}

// Read 读取 state。
//
// 行为：
//   - 文件不存在      → (nil, nil)（首次启动的正常情况，不算错）
//   - JSON 损坏       → (nil, error)（让调用方选择要不要 reset）
//   - 文件存在但为空  → (nil, nil)
//   - 权限错误        → (nil, error)
func Read() (*State, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("browserpref: 读取 state 文件失败: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("browserpref: 解析 state JSON 失败: %w", err)
	}
	// 兜底：未知 kind 视为 default，避免加新 Kind 时老 state 把"打开"逻辑弄崩。
	if s.Kind != KindChrome && s.Kind != KindDefault {
		s.Kind = KindDefault
	}
	return &s, nil
}

// Write 原子写入 state。
//
// 实现：
//   1. 确保 dataDir 存在（0755），不存在则创建
//   2. 写到同目录下 .browser_state-*.tmp
//   3. os.Rename 替换目标；Windows 上被杀毒挡住时 sleep 100ms 重试一次
//
// 失败只返回 error，不污染现有文件 —— 原子语义的一部分。
func Write(s *State) error {
	if s == nil {
		return errors.New("browserpref: state 不能为 nil")
	}
	path, err := Path()
	if err != nil {
		return err
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = time.Now()
	}

	dataDirMu.RLock()
	dir := dataDir
	dataDirMu.RUnlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("browserpref: 创建数据目录失败: %w", err)
	}

	encoded, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("browserpref: 序列化 state 失败: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".browser_state-*.tmp")
	if err != nil {
		return fmt.Errorf("browserpref: 创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // rename 成功后 Remove 返回 ErrNotExist 被忽略

	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("browserpref: 设置临时文件权限失败: %w", err)
	}
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("browserpref: 写入临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("browserpref: 关闭临时文件失败: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		// Windows 上 rename 偶尔因杀毒软件实时扫描被挡（Permission denied）。
		// 跟 credentials.saveFile 一样的兜底：100ms 后重试一次。
		if runtime.GOOS == "windows" {
			time.Sleep(100 * time.Millisecond)
			if err2 := os.Rename(tmpName, path); err2 == nil {
				return nil
			}
		}
		return fmt.Errorf("browserpref: 替换 state 文件失败: %w", err)
	}
	return nil
}

// Reset 删除 state 文件（让工具箱下次重新探测）。
//
// 文件不存在视为成功（幂等），方便 --reset-browser flag 反复调用。
func Reset() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("browserpref: 删除 state 文件失败: %w", err)
	}
	return nil
}
