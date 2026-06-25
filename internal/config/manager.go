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
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Manager 持有可热替换的 *Config，对外提供线程安全的访问入口。
type Manager struct {
	mu      sync.RWMutex
	cfg     *Config
	path    string // yaml 文件绝对路径
	baseDir string // 用于解析相对路径的基础目录（exe 所在目录）
}

// NewManager 用一份已加载的 Config 构造 Manager。
//
// 注意：cfg 必须非空；调用方负责把 Load 跑完、Defaults + Validate + ResolvePaths 通过后再传入。
func NewManager(cfg *Config, path string, baseDir string) *Manager {
	return &Manager{cfg: cfg, path: path, baseDir: baseDir}
}

// Path 返回当前 yaml 文件的绝对路径
func (m *Manager) Path() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.path
}

// Get 返回当前 Config 的只读快照副本。
//
// 返回深拷贝可避免调用方误修改快照时污染 Manager 内部状态，也避免并发 Replace
// 场景下多个调用方共享 inner slice/map 触发 data race。
func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Clone()
}

// backupFile 备份当前配置文件到带时间戳的 .bak 文件
func (m *Manager) backupFile() error {
	src, err := os.Open(m.path)
	if err != nil {
		return fmt.Errorf("打开原配置文件失败: %w", err)
	}
	defer src.Close()

	backupPath := fmt.Sprintf("%s.bak.%s", m.path, time.Now().Format("20060102-150405"))
	dst, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("创建备份文件失败: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("写入备份文件失败: %w", err)
	}
	if err := dst.Sync(); err != nil {
		return fmt.Errorf("同步备份文件失败: %w", err)
	}
	return nil
}

// ImportYAML 从 YAML 文本导入配置，替换当前配置，并备份旧文件。
//
// 流程：
//  1. yaml.Unmarshal 解析
//  2. 跑 Defaults（补缺失值）
//  3. 跑 Validate（拒非法值）
//  4. 跑 ResolvePaths（解析相对路径为绝对路径）
//  5. 跑 EnsureDirs（创建必要目录）
//  6. 备份旧文件
//  7. 原子写新 yaml
//  8. 替换内存中的 cfg
func (m *Manager) ImportYAML(yamlData []byte) error {
	var newCfg Config
	if err := yaml.Unmarshal(yamlData, &newCfg); err != nil {
		return fmt.Errorf("解析 yaml 失败: %w", err)
	}
	newCfg.Defaults()
	if err := newCfg.Validate(); err != nil {
		return fmt.Errorf("配置校验失败: %w", err)
	}
	if err := newCfg.ResolvePaths(m.baseDir); err != nil {
		return fmt.Errorf("解析路径失败: %w", err)
	}
	if err := newCfg.EnsureDirs(); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.backupFile(); err != nil {
		return fmt.Errorf("备份旧配置失败: %w", err)
	}

	path := m.path
	if err := writeYAMLAtomic(path, &newCfg); err != nil {
		return fmt.Errorf("写 yaml 失败: %w", err)
	}

	m.cfg = &newCfg
	return nil
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
	newCfg = newCfg.Clone()
	// 1+2. 标准化 + 校验
	newCfg.Defaults()
	if err := newCfg.ResolvePaths(m.baseDir); err != nil {
		return fmt.Errorf("路径解析失败: %w", err)
	}
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
	if err := os.Rename(tmpPath, path); err != nil {
		// 兼容 Windows：不能先删除原文件，否则第二次 rename 失败会导致配置文件丢失。
		// 改为 old -> backup -> new -> path；如果 new -> path 失败，尽量恢复 backup。
		backupPath := path + ".bak"
		_ = os.Remove(backupPath)
		if bakErr := os.Rename(path, backupPath); bakErr != nil {
			return fmt.Errorf("rename 失败: %w", err)
		}
		if renErr := os.Rename(tmpPath, path); renErr != nil {
			_ = os.Rename(backupPath, path)
			return fmt.Errorf("rename 失败: %w", renErr)
		}
		_ = os.Remove(backupPath)
	}
	writeOK = true
	return nil
}
