// Package downloads 管理 downloads/ 目录里的日志下载文件 + 元数据。
//
// 设计：
//   - 数据文件按日期子目录存放（downloads/YYYYMMDD/<file>），原文件名保留
//   - 元数据统一写到 downloads/.kairo-meta.json 单文件（key 是 "YYYYMMDD/<file>"）
//     避免在 downloads/ 目录里散一堆 .meta 副作用文件
//
// 这样下载历史页能直接展示"这份文件是从哪台机器、哪个目录、什么时候下来的"，
// 不依赖解析文件名约定（更稳），同时 downloads/ 目录干净。
//
// 元数据是"尽力而为"的——索引文件丢了/写失败，List 仍然能列出数据文件，
// 只是元字段为空（按目录名推断日期）。
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
	"sync"
	"time"
)

// metaIndexFile 索引文件名（隐藏在 downloads/ 根目录里）。
const metaIndexFile = ".kairo-meta.json"

// metaIndex 是元数据索引文件的结构：
//   - Version: 文件格式版本（未来加字段用）
//   - Files:   key="<date>/<file>" → Meta
type metaIndex struct {
	Version int             `json:"version"`
	Files   map[string]Meta `json:"files"`
}

// metaIndexCache 进程内缓存：避免每次 List 都读盘。
// 用 mtime 失效：写完索引后通过 atomic rename 更新 mtime，下次读会重 load。
var (
	metaCacheMu  sync.Mutex
	metaCacheDir string
	metaCache    *metaIndex
	metaCacheMT  time.Time
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
	Kind         string    `json:"kind"` // "file" / "zip"
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

// WriteMeta 把元数据写到 rootDir/.kairo-meta.json（按 "YYYYMMDD/file" 或 "file" 索引）。
// 用 0o600 权限（不准备给其它用户看）；失败不返回错误也能继续（list 仍能看到文件），
// 所以本函数不 panic，调用方按需决定是否报错。
//
// dataPath 相对于 rootDir 的两种合法形态：
//   - "<file>"：直接放 rootDir 下（兼容历史/手工拷贝）
//   - "<date>/<file>"：按日期子目录放（项 4 新约定）
func WriteMeta(dataPath string, m Meta) error {
	if m.DownloadedAt.IsZero() {
		m.DownloadedAt = time.Now()
	}
	if m.Kind == "" {
		m.Kind = "file"
	}
	rootDir, rel, err := splitDataPath(dataPath)
	if err != nil {
		return err
	}
	idx, mtime, err := loadMetaIndex(rootDir)
	if err != nil {
		return err
	}
	if idx.Files == nil {
		idx.Files = map[string]Meta{}
	}
	idx.Files[rel] = m
	return saveMetaIndex(rootDir, idx, mtime)
}

// ReadMeta 读 rootDir/.kairo-meta.json 里 dataPath 对应的元数据；不存在时返回 (zeroMeta, false, nil)。
func ReadMeta(dataPath string) (Meta, bool, error) {
	rootDir, rel, err := splitDataPath(dataPath)
	if err != nil {
		return Meta{}, false, err
	}
	idx, _, err := loadMetaIndex(rootDir)
	if err != nil {
		return Meta{}, false, err
	}
	m, ok := idx.Files[rel]
	return m, ok, nil
}

// splitDataPath 把 dataPath 拆成 (rootDir, rel)，rel 是相对 rootDir 的 "YYYYMMDD/file" 或 "file"。
//
// 算法：dataPath 可能是绝对路径或相对路径。我们先在 rootDir 候选里找
// 第一个"包含 <date>/<file> 三段"的父目录；如果都没有，就把 rootDir 整个当
// rootDir，rel = 整个相对路径。这样两种目录布局都兼容。
//
// 但因为 WriteMeta / ReadMeta / Delete 没有传 rootDir（只传 dataPath），
// 我们用更简单的策略：dataPath = rootDir/<rest>，倒推 rootDir 是包含 dataPath
// 的最大 "downloads" 目录。具体做法：把 dataPath 转为绝对路径，往上找
// 直到找到一个含 ".kairo-meta.json" 或就是传入路径的祖父目录。
//
// 这里采用最稳的方案：dataPath 必须是 rootDir/<date>/<file> 或 rootDir/<file>
// 形态，我们反推 rootDir 最多两层：
//   - 父目录名是 YYYYMMDD 格式 → 祖父是 rootDir
//   - 否则 父目录就是 rootDir
func splitDataPath(dataPath string) (rootDir, rel string, err error) {
	abs, err := filepath.Abs(dataPath)
	if err != nil {
		return "", "", fmt.Errorf("解析路径失败: %w", err)
	}
	dir := filepath.Dir(abs)   // 父目录
	base := filepath.Base(dir) // 父目录名
	if isLikelyDateDir(base) {
		// .../downloads/YYYYMMDD/<file> → rootDir=.../downloads, rel=YYYYMMDD/<file>
		rootDir = filepath.Dir(dir)
		rel = filepath.ToSlash(filepath.Join(base, filepath.Base(abs)))
		return rootDir, rel, nil
	}
	// .../downloads/<file> → rootDir=.../downloads, rel=<file>
	rootDir = dir
	rel = filepath.Base(abs)
	return rootDir, rel, nil
}

// loadMetaIndex 加载 rootDir 下的索引文件，命中 mtime 缓存。
// 返回 (idx, mtime, error)。写回时 mtime 用于乐观锁（避免多写者互相覆盖）。
func loadMetaIndex(rootDir string) (*metaIndex, time.Time, error) {
	metaCacheMu.Lock()
	defer metaCacheMu.Unlock()
	indexPath := filepath.Join(rootDir, metaIndexFile)
	st, statErr := os.Stat(indexPath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			// 文件不存在 → 空索引；同时清掉进程缓存（rootDir 切换时）
			if metaCacheDir == rootDir && metaCache != nil {
				metaCache = &metaIndex{Version: 1, Files: map[string]Meta{}}
				metaCacheMT = time.Time{}
			}
			// 项 4 修复：空索引也必须初始化 Files map（MigrateSidecars 要写入），
			// 不然触发 "assignment to entry in nil map" panic。
			return &metaIndex{Version: 1, Files: map[string]Meta{}}, time.Time{}, nil
		}
		return nil, time.Time{}, fmt.Errorf("stat 索引失败: %w", statErr)
	}
	// 命中缓存：rootDir 匹配 + mtime 一致
	if metaCacheDir == rootDir && metaCache != nil && metaCacheMT.Equal(st.ModTime()) {
		return metaCache, st.ModTime(), nil
	}
	// 失效 → 读盘
	b, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("读索引失败: %w", err)
	}
	var idx metaIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, time.Time{}, fmt.Errorf("解析索引失败: %w", err)
	}
	if idx.Files == nil {
		idx.Files = map[string]Meta{}
	}
	metaCacheDir = rootDir
	metaCache = &idx
	metaCacheMT = st.ModTime()
	return metaCache, st.ModTime(), nil
}

// saveMetaIndex 原子写回索引：先写临时文件再 rename，并发安全。
// mtimeOld 是 load 时的索引文件 mtime；如果对账时 mtime 变了（其它进程改了），
// 我们重新读 → merge → 再写一次（最多重试 3 次，避免活锁）。
func saveMetaIndex(rootDir string, idx *metaIndex, mtimeOld time.Time) error {
	indexPath := filepath.Join(rootDir, metaIndexFile)
	for attempt := 0; attempt < 3; attempt++ {
		b, err := json.MarshalIndent(idx, "", "  ")
		if err != nil {
			return fmt.Errorf("序列化索引失败: %w", err)
		}
		// 写临时文件 + rename（atomic on POSIX / best-effort on Windows）
		tmp, err := os.CreateTemp(rootDir, ".kairo-meta-*.tmp")
		if err != nil {
			return fmt.Errorf("创建临时索引失败: %w", err)
		}
		tmpName := tmp.Name()
		// 0o600 权限（跟原 sidecar 一样）
		if err := tmp.Chmod(0o600); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return fmt.Errorf("设置权限失败: %w", err)
		}
		if _, err := tmp.Write(b); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return fmt.Errorf("写临时索引失败: %w", err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("关闭临时索引失败: %w", err)
		}
		// 乐观锁：先看看 mtime 是否变了
		if !mtimeOld.IsZero() {
			if st, err := os.Stat(indexPath); err == nil && !st.ModTime().Equal(mtimeOld) {
				// 别人改了 → 重新读 + merge + 再写
				_ = os.Remove(tmpName)
				cur, _, lerr := loadMetaIndex(rootDir)
				if lerr != nil {
					return lerr
				}
				for k, v := range idx.Files {
					cur.Files[k] = v
				}
				idx = cur
				mtimeOld = st.ModTime()
				continue
			}
		}
		if err := os.Rename(tmpName, indexPath); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("rename 索引失败: %w", err)
		}
		// 失效缓存（让下次 load 重新读盘拿到新 mtime）
		metaCacheMu.Lock()
		if metaCacheDir == rootDir {
			metaCache = nil
			metaCacheMT = time.Time{}
		}
		metaCacheMu.Unlock()
		return nil
	}
	return errors.New("saveMetaIndex: 乐观锁重试超限")
}

func cloneMetaIndex(src *metaIndex) *metaIndex {
	dst := &metaIndex{Version: 1, Files: map[string]Meta{}}
	if src == nil {
		return dst
	}
	dst.Version = src.Version
	if dst.Version == 0 {
		dst.Version = 1
	}
	for k, v := range src.Files {
		dst.Files[k] = v
	}
	return dst
}

// List 列出 rootDir 下所有数据文件 + 它们的元数据。
//
//   - 按 mtime 倒序；
//   - 跳过隐藏文件（. 开头，含 .kairo-meta.json 索引文件）；
//   - 子目录（如 downloads/20260621/）递归；
//   - rootDir 不存在时返回空切片和 nil；
//   - 列表策略：优先列有元数据的文件（确认是 Kairo 下载产物）；
//     没元数据的兜底按扩展名收（.log / .zip / .txt / .gz / .tar / .properties 等常见后缀），
//     避免 v0.3 文件浏览器下载的 .properties / .xml 等"任意文件"不显示在历史里。
//
// 兜底推断（项 16）：
//   - 如果文件位于 downloads/YYYYMMDD/ 子目录但没有元数据，把子目录名当 Date；
//   - 这样手动 cp 进来的文件也能在历史里看到（虽然元数据为空）。
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
		// 跳过元数据索引文件（以防用户手动复制时被 WalkDir 撞到）
		if name == metaIndexFile {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(rootDir, path)
		meta, hasMeta, _ := ReadMeta(path)
		// 决定是否列出 + kind 标签：
		//   1. 有元数据：列出，kind 来自 meta
		//   2. 无元数据：按扩展名兜底列常见文件类型，kind="file"（或 zip）
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
		// 兜底推断：无元数据时用文件 mtime 作为下载时间（比从目录名推断 00:00:00 更准）。
		if !hasMeta {
			meta.DownloadedAt = info.ModTime()
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

// isLikelyDateDir 判断一个目录名是否是日期格式（YYYYMMDD 或 YYYY-MM-DD）。
func isLikelyDateDir(name string) bool {
	if len(name) == 8 {
		for i := 0; i < 8; i++ {
			if name[i] < '0' || name[i] > '9' {
				return false
			}
		}
		return true
	}
	if len(name) == 10 && name[4] == '-' && name[7] == '-' {
		for i := 0; i < 10; i++ {
			if i == 4 || i == 7 {
				continue
			}
			if name[i] < '0' || name[i] > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// parseDateDirAsTime 把 YYYYMMDD / YYYY-MM-DD 解析成 time.Time（本地时区 00:00）。
// 解析失败返回 zero time。
func parseDateDirAsTime(name string) time.Time {
	if len(name) == 10 {
		name = strings.ReplaceAll(name, "-", "")
	}
	if len(name) != 8 {
		return time.Time{}
	}
	t, err := time.ParseInLocation("20060102", name, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
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
		".ds_store":   true, // macOS 资源管理器
		".tmp":        true,
		".temp":       true,
		".swp":        true, // vim swap
		".bak":        true,
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

// Delete 删除一个文件 + 从元数据索引里移除对应条目。
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
	// 从索引里删条目（不存在也没关系 —— 文件没了元数据也无意义）
	removeFromIndex(rootDir, name)
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
	// 先把所有要删的 name 收集起来，一次性更新索引（避免每删一次都 load/save）
	toRemove := make([]string, 0, len(entries))
	for _, e := range entries {
		if err := os.Remove(e.Path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return n, fmt.Errorf("删除 %s 失败: %w", e.Name, err)
		}
		toRemove = append(toRemove, e.Name)
		n++
	}
	if len(toRemove) > 0 {
		removeFromIndexBulk(rootDir, toRemove)
	}
	return n, nil
}

// removeFromIndex 从 rootDir 的索引文件里移除单条 key。
func removeFromIndex(rootDir, relName string) {
	idx, mtime, err := loadMetaIndex(rootDir)
	if err != nil || idx == nil || len(idx.Files) == 0 {
		return
	}
	idx = cloneMetaIndex(idx)
	delete(idx.Files, filepath.ToSlash(relName))
	_ = saveMetaIndex(rootDir, idx, mtime)
}

// removeFromIndexBulk 批量删除：避免 N 次 IO 抖动。
func removeFromIndexBulk(rootDir string, names []string) {
	idx, mtime, err := loadMetaIndex(rootDir)
	if err != nil || idx == nil || len(idx.Files) == 0 {
		return
	}
	idx = cloneMetaIndex(idx)
	for _, n := range names {
		delete(idx.Files, filepath.ToSlash(n))
	}
	_ = saveMetaIndex(rootDir, idx, mtime)
}

// MigrateSidecars 从老的 .meta sidecar 格式迁移到单文件索引。
//
// 一次性工具：扫描 rootDir 下所有 <name>.meta 文件，把内容合并到
// .kairo-meta.json 索引文件，然后删掉 sidecar。
//
// 调用方：CLI 命令（root 启动时一次性跑一次）；或管理员手动。
// 幂等：跑过一次后再跑没副作用（sidecar 已经删完，索引文件已存在）。
//
// 返回：迁移的 sidecar 数量、跳过的数量、错误。
func MigrateSidecars(rootDir string) (migrated, skipped int, err error) {
	idx, mtime, err := loadMetaIndex(rootDir)
	if err != nil {
		return 0, 0, err
	}
	idx = cloneMetaIndex(idx)
	walkErr := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".meta") {
			return nil
		}
		// 跳过 .kairo-meta.json 自身（万一以后改后缀）
		if name == metaIndexFile {
			return nil
		}
		// 读 sidecar
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			skipped++
			return nil
		}
		var m Meta
		if jerr := json.Unmarshal(b, &m); jerr != nil {
			skipped++
			return nil
		}
		// 算 dataPath 相对 key（"YYYYMMDD/file" 或 "file"）
		dataPath := strings.TrimSuffix(path, ".meta")
		rel, _ := filepath.Rel(rootDir, dataPath)
		relSlash := filepath.ToSlash(rel)
		// 合并到索引
		if existing, ok := idx.Files[relSlash]; ok {
			// 已存在 → 用更完整的（保留 DownloadedAt 早的）
			if existing.DownloadedAt.IsZero() || (!m.DownloadedAt.IsZero() && m.DownloadedAt.Before(existing.DownloadedAt)) {
				idx.Files[relSlash] = m
			}
		} else {
			idx.Files[relSlash] = m
		}
		// 删 sidecar
		if rerr := os.Remove(path); rerr == nil {
			migrated++
		} else {
			skipped++
		}
		return nil
	})
	if walkErr != nil {
		return migrated, skipped, fmt.Errorf("遍历失败: %w", walkErr)
	}
	if err := saveMetaIndex(rootDir, idx, mtime); err != nil {
		return migrated, skipped, fmt.Errorf("保存索引失败: %w", err)
	}
	return migrated, skipped, nil
}

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

// CleanupResult 清理结果统计
type CleanupResult struct {
	RetentionDays  int     // 使用的保留天数配置
	MaxCount       int     // 使用的最大记录数配置
	DeletedByAge   int     // 按过期天数删除的数量
	DeletedByCount int     // 按数量限制删除的数量
	TotalDeleted   int     // 总共删除的数量
	Errors         []error // 清理过程中遇到的错误
}

// Cleanup 根据配置清理过期下载文件和记录。
//
// 参数：
//   - rootDir: 下载根目录
//   - retentionDays: 保留天数；0 = 不按时间清理
//   - maxCount: 最大记录数；0 = 不限制数量
//
// 清理策略：
//  1. 先按 retentionDays 删除所有早于 (now - retentionDays*24h) 的记录和文件；
//  2. 再按 maxCount 删除最旧的记录，使总数不超过 maxCount；
//  3. 删除文件后，同步从元数据索引中移除对应条目；
//  4. 最后尝试清理空的日期子目录。
func Cleanup(rootDir string, retentionDays, maxCount int) CleanupResult {
	result := CleanupResult{
		RetentionDays: retentionDays,
		MaxCount:      maxCount,
	}

	entries, err := List(rootDir)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("列出下载记录失败: %w", err))
		return result
	}

	if len(entries) == 0 {
		return result
	}

	// 按时间从旧到新排序（List 返回的是从新到旧）
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ModTime.Before(entries[j].ModTime)
	})

	now := time.Now()
	cutoff := now.AddDate(0, 0, -retentionDays)
	toDelete := make(map[string]Entry) // key 是相对路径 Name

	// 1. 按过期天数清理
	if retentionDays > 0 {
		for _, e := range entries {
			// 使用 Meta.DownloadedAt（如果有），否则用文件 ModTime
			t := e.Meta.DownloadedAt
			if t.IsZero() {
				t = e.ModTime
			}
			if t.Before(cutoff) {
				toDelete[e.Name] = e
			}
		}
		result.DeletedByAge = len(toDelete)
	}

	// 2. 按数量限制清理（总数 - 已删 - 保留 = 还要删的最旧记录数）
	if maxCount > 0 {
		remaining := make([]Entry, 0, len(entries))
		for _, e := range entries {
			if _, del := toDelete[e.Name]; !del {
				remaining = append(remaining, e)
			}
		}
		if len(remaining) > maxCount {
			// remaining 已经按时间升序（旧→新），删除前 len(remaining)-maxCount 个最旧的
			needDelete := len(remaining) - maxCount
			for i := 0; i < needDelete; i++ {
				toDelete[remaining[i].Name] = remaining[i]
				result.DeletedByCount++
			}
		}
	}

	if len(toDelete) == 0 {
		return result
	}

	// 3. 执行删除：先收集所有要删除的 name，再批量更新索引
	namesToRemove := make([]string, 0, len(toDelete))
	for name, e := range toDelete {
		if err := os.Remove(e.Path); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				result.Errors = append(result.Errors, fmt.Errorf("删除文件 %s 失败: %w", name, err))
				continue
			}
		}
		namesToRemove = append(namesToRemove, name)
		result.TotalDeleted++
	}

	// 4. 批量从索引中移除
	if len(namesToRemove) > 0 {
		removeFromIndexBulk(rootDir, namesToRemove)
	}

	// 5. 清理空的日期子目录
	cleanupEmptyDirs(rootDir)

	return result
}

// cleanupEmptyDirs 删除 rootDir 下所有空的日期子目录（YYYYMMDD / YYYY-MM-DD 格式）。
func cleanupEmptyDirs(rootDir string) {
	dirs, err := os.ReadDir(rootDir)
	if err != nil {
		return
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		name := d.Name()
		if !isLikelyDateDir(name) {
			continue
		}
		dirPath := filepath.Join(rootDir, name)
		items, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		hasFiles := false
		for _, item := range items {
			// 跳过隐藏文件和元数据索引文件（理论上索引只在 rootDir，不会在子目录）
			if !item.IsDir() && !strings.HasPrefix(item.Name(), ".") {
				hasFiles = true
				break
			}
		}
		if !hasFiles {
			_ = os.Remove(dirPath)
		}
	}
}
