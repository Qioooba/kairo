// Package config / manager.go
//
// 线程安全的 Config 持有者。
//
// 设计目标：
//   - 启动时一次性 Load，运行时页面修改后直接生效（不需要重启进程）；
//   - 整树替换：每次写入都构造一个全新的 *Config 替换进去，
//     所有 reader 看到的是同一个不可变快照，零脏读；
//   - 写盘用 y.Marshal + 文件级原子替换（写 .tmp + rename），
//     避免崩溃时残留半截 yaml；
//   - 写盘时跑一遍 Validate，拒掉非法值后再落到磁盘。
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Manager 持有可热替换的 *Config，对外提供线程安全的访问入口。
type Manager struct {
	mu   sync.RWMutex
	cfg  *Config
	path string // yaml 文件绝对路径
}

// NewManager 用一份已加载的 Config 构造 Manager。
//
// 注意：cfg 必须非空；调用方负责把 Load 跑完、Defaults + Validate 通过后再传入。
func NewManager(cfg *Config, path string) *Manager {
	return &Manager{cfg: cfg, path: path}
}

// Path 返回当前 yaml 文件的绝对路径
func (m *Manager) Path() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.path
}

// Get 返回当前 Config 的只读快照。
//
// 注意：返回的 *Config 在 Manager 看来是 immutable 的；
// 调用方只能读，不应该写任何字段。需要改时走 Replace。
func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// Replace 用一份新的 Config 整树替换，并原子写回 yaml。
//
// 流程：
//  1. 跑 Defaults（补缺失值）
//  2. 跑 Validate（拒非法值）
//  3. 写 .tmp + os.Rename 原子替换
//  4. 替换内存中的 cfg
func (m *Manager) Replace(newCfg *Config) error {
	if newCfg == nil {
		return errors.New("配置为空")
	}
	// 1+2. 标准化 + 校验
	newCfg.Defaults()
	if err := newCfg.Validate(); err != nil {
		return fmt.Errorf("配置校验失败: %w", err)
	}
	// 3. 写盘
	m.mu.Lock()
	path := m.path
	if err := writeYAMLAtomic(path, newCfg); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("写 yaml 失败: %w", err)
	}
	// 4. 替换内存引用
	m.cfg = newCfg
	m.mu.Unlock()
	return nil
}

// writeYAMLAtomic 原子写 yaml：写到 .tmp 然后 rename。
//
// 为什么不用 yaml.Marshal+os.WriteFile？
//   - 写一半崩溃会留下半截 yaml，下一次 Load 直接报错；
//   - rename 是 POSIX 原子操作（同分区），保证读到的是"上一份完整文件"或"新完整文件"。
func writeYAMLAtomic(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化 yaml 失败: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	// 写失败要清理
	writeOK := false
	defer func() {
		if !writeOK {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写临时文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync 失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	// Windows 上 rename 不会覆盖已有文件；先删后改。
	if err := os.Rename(tmpPath, path); err != nil {
		// 兼容 Windows：先删旧文件再 rename
		if rmErr := os.Remove(path); rmErr == nil {
			if err := os.Rename(tmpPath, path); err != nil {
				return fmt.Errorf("rename 失败: %w", err)
			}
		} else {
			return fmt.Errorf("rename 失败: %w", err)
		}
	}
	writeOK = true
	return nil
}
