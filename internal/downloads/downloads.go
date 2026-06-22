// Package downloads 管理 downloads/ 目录里的日志下载文件 + 元数据 sidecar。
//
// 设计：每个数据文件（log / zip）旁边都放一个 .meta JSON 文件，记录：
//   - 来源服务器、远端路径、原始文件名、编码、下载时间
//
// 这样下载历史页能直接展示"这份文件是从哪台机器、哪个目录、什么时候下来的"，
// 不依赖解析文件名约定（更稳）。
//
// 文件名约定：
//   - 数据文件：<name>        （如 mock-node-1_server1_SystemOut.log_133800.log）
//   - 元数据：  <name>.meta   （如 mock-node-1_server1_SystemOut.log_133800.log.meta）
//
// 注意：元数据是"尽力而为"的——如果 sidecar 丢了，List 仍然能列出数据文件，
// 只是元字段为空。删除数据文件时同时删 sidecar。
package downloads

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Meta 元数据 sidecar 内容
type Meta struct {
	System       string    `json:"system"`              // 配置里的业务系统名
	Server       string    `json:"server"`              // 配置里的 server 别名
	Host         string    `json:"host,omitempty"`      // host:port（仅展示用）
	Dir          string    `json:"dir"`                 // 远端日志目录
	DirAlias     string    `json:"dir_alias,omitempty"` // 远端目录最后一段
	File         string    `json:"file,omitempty"`      // 远端原始文件名（zip 时为空）
	Files        []string  `json:"files,omitempty"`     // zip 包含的远端文件名
	Encoding     string    `json:"encoding,omitempty"`
	Kind         string    `json:"kind"`                // "file" / "zip"
	DownloadedAt time.Time `json:"downloaded_at"`
	DownloadedBy string    `json:"downloaded_by,omitempty"` // 操作人（暂留字段）
}

// Entry List 返回的一条记录
type Entry struct {
	Name        string    `json:"name"` // basename（用于 /downloads/<name>）
	Path        string    `json:"path"` // 磁盘绝对路径
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	Kind        string    `json:"kind"`         // "file" / "zip" / "unknown"
	Meta        Meta      `json:"meta"`         // 解析自 sidecar；缺失时为零值
	MetaPresent bool      `json:"meta_present"` // sidecar 是否存在
}

// WriteMeta 把元数据写到 <dataPath>.meta。
// 用 0o600 权限（不准备给其它用户看）；失败不返回错误也能继续（list 仍能看到文件），
// 所以本函数不 panic，调用方按需决定是否报错。
func WriteMeta(dataPath string, m Meta) error {
	if m.DownloadedAt.IsZero() {
		m.DownloadedAt = time.Now()
	}
	if m.Kind == "" {
		m.Kind = "file"
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化元数据失败: %w", err)
	}
	side := sidecarPath(dataPath)
	if err := os.WriteFile(side, b, 0o600); err != nil {
		return fmt.Errorf("写入 sidecar 失败: %w", err)
	}
	return nil
}

// ReadMeta 读 <dataPath> 的 sidecar；不存在时返回 (zeroMeta, false, nil)。
func ReadMeta(dataPath string) (Meta, bool, error) {
	side := sidecarPath(dataPath)
	b, err := os.ReadFile(side)
	if err != nil {
		if os.IsNotExist(err) {
			return Meta{}, false, nil
		}
		return Meta{}, false, fmt.Errorf("读 sidecar 失败: %w", err)
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Meta{}, false, fmt.Errorf("解析 sidecar 失败: %w", err)
	}
	return m, true, nil
}

// List 列出 rootDir 下所有数据文件（不含 .meta）+ 它们的元数据。
//
//   - 按 mtime 倒序；
//   - 跳过隐藏文件（. 开头）和 .meta sidecar；
//   - 子目录（如 downloads/20260621/）递归；
//   - rootDir 不存在时返回空切片和 nil；
//   - 列表策略：优先列有 sidecar 元数据的文件（确认是 ops-toolbox 下载产物）；
//     没 sidecar 的兜底按扩展名收（.log / .zip / .txt / .gz / .tar / .properties 等常见后缀），
//     避免 v0.3 文件浏览器下载的 .properties / .xml 等"任意文件"不显示在历史里。
func List(rootDir string) ([]Entry, error) {
	var out []Entry
	if _, err := os.Stat(rootDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat 失败: %w", err)
	}
	walkErr := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过不可访问的子目录
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			return nil
		}
		// 跳过 sidecar
		if strings.HasSuffix(name, ".meta") {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(rootDir, path)
		meta, hasMeta, _ := ReadMeta(path)
		// 决定是否列出 + kind 标签：
		//   1. 有 sidecar：列出，kind 来自 meta
		//   2. 无 sidecar：按扩展名兜底列常见文件类型，kind="file"（或 zip）
		if !hasMeta {
			ext := strings.ToLower(filepath.Ext(name))
			if !isListableExt(ext) {
				return nil
			}
		}
		kind := "unknown"
		if hasMeta && meta.Kind != "" {
			kind = meta.Kind
		} else if strings.EqualFold(filepath.Ext(name), ".zip") {
			kind = "zip"
		} else {
			kind = "file"
		}
		out = append(out, Entry{
			Name:        filepath.ToSlash(rel),
			Path:        path,
			Size:        info.Size(),
			ModTime:     info.ModTime(),
			Kind:        kind,
			Meta:        meta,
			MetaPresent: hasMeta,
		})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("遍历失败: %w", walkErr)
	}
	// 按 mtime 倒序
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out, nil
}

// isListableExt 决定"无 sidecar 时"按扩展名兜底列哪些文件。
//
// 设计：
//   - 显式 list 常见日志/压缩/配置/包文件（运维工具常见产物）；
//   - 显式 deny 系统临时文件（.DS_Store / 各种临时）；
//   - 不在大名单里、也不是 deny 名单的，按 allow（白名单 + 黑名单之外的默认拒绝）。
//
// 用 deny + allow 两段式，避免把无关文件（如 .html / .bin）误列为"下载历史"。
func isListableExt(ext string) bool {
	// 无扩展名：不要（避免把 README 之类误列）
	if ext == "" {
		return false
	}
	// 显式拒绝：常见的无关临时 / 系统文件
	deny := map[string]bool{
		".ds_store": true, // macOS 资源管理器
		".tmp":      true,
		".temp":     true,
		".swp":      true, // vim swap
		".bak":      true,
		".crdownload": true, // Chrome 残留
	}
	if deny[ext] {
		return false
	}
	// 显式允许：运维 / 下载常见扩展名
	allow := map[string]bool{
		".log":        true,
		".zip":        true,
		".txt":        true,
		".gz":         true,
		".tar":        true,
		".tgz":        true,
		".tar.gz":     true, // 实际 ext 只是 .gz，已在前面覆盖
		".bz2":        true,
		".xz":         true,
		".7z":         true,
		".rar":        true,
		".out":        true,
		".err":        true,
		".trace":      true,
		".properties": true,
		".xml":        true,
		".json":       true,
		".yml":        true,
		".yaml":       true,
		".conf":       true,
		".cfg":        true,
		".ini":        true,
		".sql":        true,
		".csv":        true,
		".jar":        true,
		".war":        true,
		".ear":        true,
		".class":      true,
		".dump":       true,
		".heapdump":   true,
		".dat":        true,
		".pid":        true,
	}
	return allow[ext]
}

// Delete 删除一个文件 + 它的 sidecar。
//   - name 必须是相对 rootDir 的路径，不允许含 ".." 或绝对路径；
//   - 删除后必须仍然在 rootDir 范围内（防穿越）。
func Delete(rootDir, name string) error {
	abs, err := safeJoin(rootDir, name)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // 已经不在了
		}
		return fmt.Errorf("删除文件失败: %w", err)
	}
	// 删 sidecar（不存在也没关系）
	_ = os.Remove(sidecarPath(abs))
	return nil
}

// DeleteAll 删除 rootDir 下所有数据文件（保留子目录结构，留 audit 用）。
//   - 不会删 rootDir 本身；
//   - 返回删除的文件数。
func DeleteAll(rootDir string) (int, error) {
	entries, err := List(rootDir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if err := os.Remove(e.Path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return n, fmt.Errorf("删除 %s 失败: %w", e.Name, err)
		}
		_ = os.Remove(sidecarPath(e.Path))
		n++
	}
	return n, nil
}

// sidecarPath 返回 dataPath 对应的 sidecar 路径（<path>.meta）。
func sidecarPath(dataPath string) string { return dataPath + ".meta" }

// safeJoin 把 name 安全拼到 rootDir 下（防 ../ 穿越）。
func safeJoin(rootDir, name string) (string, error) {
	if name == "" {
		return "", errors.New("name 不能为空")
	}
	if strings.Contains(name, "\\") || filepath.IsAbs(name) {
		return "", errors.New("非法路径")
	}
	full := filepath.Join(rootDir, filepath.FromSlash(name))
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	base, err := filepath.Abs(rootDir)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, base+string(os.PathSeparator)) && abs != base {
		return "", errors.New("路径越界")
	}
	return abs, nil
}
