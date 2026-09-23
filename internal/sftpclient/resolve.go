package sftpclient

// resolve.go — OTH-03 / OTH-04 / OTH-06：远端路径解析的错误分类、逐段解析与有界索引。
//
// 背景：SFTP 协议里的文件名是字节串，老 AIX / GBK locale 的机器磁盘上是 GBK 字节，
// 展示层把它解成 UTF-8 中文，于是同一个展示名可能对应两条不同的物理条目。
// 旧实现（resolveExistingDir / resolveWritePath）把解析错误统一降级成
// "用未解析的展示路径继续"或"顺序 Stat 取第一个候选"，导致歧义被静默吞掉、
// 写操作落到错误的物理文件上（审核 OTH-03），并且 GBK 父目录下的多级新建目录
// 会丢掉已解析前缀（OTH-04），每个 ASCII Stat 还会完整枚举父目录（OTH-06）。
//
// 本文件把解析收敛成一条共享流水线：
//
//	1. 目录解析结果分四类：唯一存在 / 确定不存在(errors.Is(os.ErrNotExist)) /
//	   歧义(errors.Is(ErrAmbiguousPath)) / 权限·网络·协议错误（原样透传）。
//	2. 逐段解析：已存在前缀按"展示段 → 原始字节段"解析，且只在唯一命中时前进；
//	   多个候选一律 ErrAmbiguousPath，绝不任选一个。
//	3. 目录创建解析（resolveDirectoryForCreate）：已存在前缀逐段解析，
//	   第一个确定不存在的段之后按"已解析原始父路径的编码"编码新段，
//	   不对混合 UTF-8/GBK 的完整绝对路径整体转码，也不丢弃已解析前缀。
//	4. 写路径（resolveWritePath）：父目录必须唯一解析成功，且必须拿到
//	   完整唯一的原始父路径 + 原始文件名；任何非"确定不存在"的错误在写入前终止。
//	5. 性能：唯一父目录 + 纯 ASCII basename 直接 Stat，不枚举父目录；
//	   非 ASCII 才走按"原始父目录"缓存的有界索引（缓存只是性能辅助，
//	   最终存在性/版本校验仍然访问远端；写操作一律不使用缓存）。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// 有界目录索引的容量与有效期。
//
// 索引键是"原始目录绝对路径"，放在 Client 上 → 天然按连接隔离。
// 只用于解析（展示名 → 原始名），不作为存在性的最终依据：
// 命中后调用方仍会对解析结果做真正的 Stat/Open。写操作会让受影响目录失效。
const (
	dirIndexMaxDirs    = 64
	dirIndexTTL        = 5 * time.Second
	dirIndexMaxEntries = 20000
)

// dirNames 是一个原始目录的"展示名 / 原始名 → 原始名集合"索引。
// 值用切片而不是单值：同显示名多编码必须能被识别成歧义。
type dirNames struct {
	byRaw     map[string][]string
	byDisplay map[string][]string
}

type dirIndexEntry struct {
	names *dirNames
	at    time.Time
}

// ctxErr 统一 nil ctx 的兜底：解析过程中任何一次远端调用前都可以检查它。
func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// isASCIIBytes 判断字符串是否纯 ASCII。
// ASCII 段在 UTF-8 与 GB18030 下字节完全一致，所以 ASCII 名字不需要双编码候选。
func isASCIIBytes(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// parentDir 是 path.Dir 的小包装：sftpclient.go 里部分方法的形参就叫 path，
// 会把包名遮蔽掉，用这个 helper 避免歧义。
func parentDir(p string) string { return path.Dir(p) }

// ambiguousErr 拼一个统一的歧义错误，带上人类可读的展示路径（解码后）。
func ambiguousErr(display string, n int) error {
	return fmt.Errorf("%w: %q (存在 %d 个同名不同编码条目)", ErrAmbiguousPath, DecodeServerName(display), n)
}

// ----------------------------------------------------------------------------
// 目录解析：唯一 / 不存在 / 歧义 / 其它错误
// ----------------------------------------------------------------------------

// resolveDir 把展示目录解析成唯一的原始字节绝对路径。
//
// 返回错误分类（调用方用 errors.Is 判定，不要看文案）：
//   - nil                 → 唯一存在
//   - os.ErrNotExist      → 确定不存在
//   - ErrAmbiguousPath    → 歧义（同显示名多编码）
//   - 其它                → 权限 / 超时 / 协议错误，必须原样透传
func (c *Client) resolveDir(ctx context.Context, dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	if raw, ok := DecodePathIdentity(dir); ok {
		return raw, nil
	}
	dir = path.Clean(dir)
	if dir == "/" || dir == "." {
		return "/", nil
	}
	if err := ctxErr(ctx); err != nil {
		return "", err
	}

	// 1. 整段候选快速命中：单候选（纯 ASCII）一次 Stat；双候选（UTF-8/GBK）
	//    两边都在就是歧义，不允许挑第一个。
	cands := EncodePathCandidates(dir)
	var matches []string
	var lastErr error
	for _, cand := range cands {
		if err := ctxErr(ctx); err != nil {
			return "", err
		}
		fi, err := c.b.Stat(cand)
		if err == nil {
			if fi.IsDir() {
				matches = append(matches, cand)
			}
			continue
		}
		lastErr = err
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", ambiguousErr(dir, len(matches))
	}

	// 2. 逐段解析：父级用已解析的原始路径，子段继续解析。
	raw, err := c.walkDirSegments(ctx, dir)
	if err != nil {
		return "", err
	}
	if raw != "" {
		return raw, nil
	}
	if lastErr != nil && !errors.Is(lastErr, os.ErrNotExist) {
		// 整段 Stat 撞上权限/协议错误且逐段也没能确认存在 → 透传原错误。
		return "", lastErr
	}
	return "", &os.PathError{Op: "stat", Path: dir, Err: os.ErrNotExist}
}

// walkDirSegments 从根开始逐段解析 dir，返回已解析的原始绝对路径。
// 任一段歧义或非 NotExist 错误立即返回错误；确定不存在返回 os.ErrNotExist。
func (c *Client) walkDirSegments(ctx context.Context, dir string) (string, error) {
	cur := "/"
	for _, seg := range splitPathSegments(dir) {
		if err := ctxErr(ctx); err != nil {
			return "", err
		}
		next, err := c.resolveChildPath(ctx, cur, seg, true)
		if err != nil {
			return "", err
		}
		cur = next
	}
	return cur, nil
}

// splitPathSegments 把绝对路径切成非空段（忽略 "."）。
func splitPathSegments(p string) []string {
	raw := strings.Split(strings.TrimPrefix(path.Clean(p), "/"), "/")
	out := raw[:0]
	for _, seg := range raw {
		if seg == "" || seg == "." {
			continue
		}
		out = append(out, seg)
	}
	return out
}

// resolveChildPath 在已解析的原始父目录 rawParent 下解析展示段 seg，
// 返回该条目的原始绝对路径。
//
// allowCache 只允许读路径使用有界索引；写路径必须传 false（避免用旧索引写入）。
//
// 顺序：
//  1. 纯 ASCII 段：两种编码字节相同 → 直接 Stat 同名，不枚举父目录（OTH-06）。
//  2. 非 ASCII 段：枚举父目录（可复用索引）→ 按原始名字节 / 展示名唯一命中。
//  3. 枚举失败：按编码候选逐个 Stat 兜底，但多候选命中一律报歧义（不得任选）。
func (c *Client) resolveChildPath(ctx context.Context, rawParent, seg string, allowCache bool) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	target := path.Join(rawParent, seg)

	if isASCIIBytes(seg) {
		if _, err := c.b.Stat(target); err == nil {
			return target, nil
		} else if errors.Is(err, os.ErrNotExist) {
			return "", &os.PathError{Op: "stat", Path: target, Err: os.ErrNotExist}
		} else {
			return "", err
		}
	}

	names, err := c.listDirNames(ctx, rawParent, allowCache)
	if err == nil {
		// 段是"展示名"还是"原始字节"决定了查哪张表：
		//   - 合法 UTF-8（含 ASCII）→ 按展示名查：同一展示名下的所有原始名都必须
		//     参与唯一性判定，否则"UTF-8 名正好等于展示名"会掩盖 GBK 兄弟条目的歧义。
		//   - 非法 UTF-8 → 调用方给的就是原始字节，按原始名字节精确查。
		var raws []string
		if utf8.ValidString(seg) {
			raws = names.byDisplay[seg]
		} else {
			raws = names.byRaw[seg]
		}
		if len(raws) == 1 {
			return path.Join(rawParent, raws[0]), nil
		}
		if len(raws) > 1 {
			return "", ambiguousErr(target, len(raws))
		}
		return "", &os.PathError{Op: "stat", Path: target, Err: os.ErrNotExist}
	}

	// 枚举失败（权限 / 协议 / 网络）：Stat 候选兜底，但必须保留歧义。
	var hits []string
	for _, cand := range EncodePathCandidates(target) {
		if err := ctxErr(ctx); err != nil {
			return "", err
		}
		if _, serr := c.b.Stat(cand); serr == nil {
			hits = append(hits, cand)
		}
	}
	if len(hits) > 1 {
		return "", ambiguousErr(target, len(hits))
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	// 无法确认"不存在"：把枚举错误透传，调用方不得把它当成 NotExist。
	return "", err
}

// ----------------------------------------------------------------------------
// 有界目录索引（性能辅助，不是正确性依据）
// ----------------------------------------------------------------------------

// listDirNames 枚举原始目录并建立展示名/原始名索引。
// allowCache=true 时复用 Client 级有界索引（TTL + 条目上限 + 写操作失效）。
func (c *Client) listDirNames(ctx context.Context, rawDir string, allowCache bool) (*dirNames, error) {
	if allowCache {
		c.idxMu.Lock()
		if c.idx != nil {
			if ent := c.idx[rawDir]; ent != nil && time.Since(ent.at) < dirIndexTTL {
				names := ent.names
				c.idxMu.Unlock()
				return names, nil
			}
		}
		c.idxMu.Unlock()
	}

	entries, err := c.b.ReadDir(rawDir)
	if err != nil {
		return nil, err
	}
	names := &dirNames{
		byRaw:     make(map[string][]string, len(entries)),
		byDisplay: make(map[string][]string, len(entries)),
	}
	for _, e := range entries {
		if e == nil {
			continue
		}
		raw := e.Name()
		names.byRaw[raw] = append(names.byRaw[raw], raw)
		dec := DecodeServerName(raw)
		names.byDisplay[dec] = append(names.byDisplay[dec], raw)
	}
	if allowCache && len(entries) <= dirIndexMaxEntries {
		c.storeDirIndex(rawDir, names)
	}
	return names, nil
}

func (c *Client) storeDirIndex(rawDir string, names *dirNames) {
	c.idxMu.Lock()
	defer c.idxMu.Unlock()
	if c.idx == nil {
		c.idx = make(map[string]*dirIndexEntry)
	}
	// 简单容量控制：超限时先清掉过期项，仍超限就整体丢弃重建。
	if len(c.idx) >= dirIndexMaxDirs {
		now := time.Now()
		for k, ent := range c.idx {
			if now.Sub(ent.at) >= dirIndexTTL {
				delete(c.idx, k)
			}
		}
		if len(c.idx) >= dirIndexMaxDirs {
			c.idx = make(map[string]*dirIndexEntry)
		}
	}
	c.idx[rawDir] = &dirIndexEntry{names: names, at: time.Now()}
}

// invalidateDirIndex 让指定原始目录（及其祖先，因为新建/删除会改变父目录列表）失效。
func (c *Client) invalidateDirIndex(dirs ...string) {
	c.idxMu.Lock()
	defer c.idxMu.Unlock()
	if len(c.idx) == 0 {
		return
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		for cur := path.Clean(d); ; cur = path.Dir(cur) {
			delete(c.idx, cur)
			if cur == "/" || cur == "." || cur == path.Dir(cur) {
				break
			}
		}
	}
}

// ----------------------------------------------------------------------------
// 读路径解析
// ----------------------------------------------------------------------------

// resolveExistingPathCtx 解析读操作的展示路径为唯一原始绝对路径。
//
//   - 身份 token：直接解码，零远端调用。
//   - 父目录唯一解析 + ASCII basename：一次 Stat 决定（不回退、不枚举）。
//   - 父目录唯一解析 + 非 ASCII：枚举（可用索引）后唯一命中；不存在即不存在。
//   - 父目录无法唯一解析（歧义）：透传 ErrAmbiguousPath。
//   - 父目录/条目解析失败：按"整条展示路径的编码候选"兜底 Stat，
//     但多候选命中一律报歧义（这是审核要求的"不能借 fallback 任选候选"）。
func (c *Client) resolveExistingPathCtx(ctx context.Context, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	if raw, ok := DecodePathIdentity(p); ok {
		return raw, nil
	}
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	p = path.Clean(p)
	if p == "/" || p == "." {
		return p, nil
	}

	parent := path.Dir(p)
	base := path.Base(p)

	rawParent, perr := c.resolveDir(ctx, parent)
	if perr == nil {
		if isASCIIBytes(base) {
			target := path.Join(rawParent, base)
			if _, err := c.b.Stat(target); err == nil {
				return target, nil
			} else if errors.Is(err, os.ErrNotExist) {
				return "", &os.PathError{Op: "stat", Path: p, Err: os.ErrNotExist}
			} else {
				return "", err
			}
		}
		resolved, err := c.resolveChildPath(ctx, rawParent, base, true)
		if err == nil {
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "", &os.PathError{Op: "stat", Path: p, Err: os.ErrNotExist}
	}
	// 父目录歧义 / 确定不存在 / 无法判定（权限等）：统一走"歧义可见"的候选兜底。
	// 这样既保留 shell backend 等"Stat 可用但 ReadDir 不可用"场景的兼容性，
	// 又不会把歧义降级成任选一个候选。
	return c.statCandidatesForRead(ctx, p, perr)
}

// statCandidatesForRead 对整条展示路径的编码候选逐个 Stat。
// 唯一命中返回命中路径；多命中报歧义；零命中返回 fallback（父目录解析错误）。
//
// 单候选（纯 ASCII）也 Stat 一次：这保留"Stat 可用但 ReadDir/父级不可用"
// 场景（shell 兜底、受限目录）的读兼容性；写路径不经过这里。
func (c *Client) statCandidatesForRead(ctx context.Context, display string, fallback error) (string, error) {
	var hits []string
	for _, cand := range EncodePathCandidates(display) {
		if err := ctxErr(ctx); err != nil {
			return "", err
		}
		if _, err := c.b.Stat(cand); err == nil {
			hits = append(hits, cand)
		}
	}
	if len(hits) > 1 {
		return "", ambiguousErr(display, len(hits))
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	return "", fallback
}

// ----------------------------------------------------------------------------
// 写路径解析
// ----------------------------------------------------------------------------

// resolveWritePathCtx 解析写操作的目标路径。
//
// 与读路径的关键差别：这里**没有**"候选兜底"。父目录歧义、权限/协议错误、
// 确定不存在都在写入前终止——普通文件写路径不得把失败降级成
// "用未解析的展示路径继续写"（否则就是审核实测的静默覆盖另一个物理文件）。
// 需要创建父目录的场景必须走 resolveDirectoryForCreate / MkdirAll。
func (c *Client) resolveWritePathCtx(ctx context.Context, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	if raw, ok := DecodePathIdentity(p); ok {
		return raw, nil
	}
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	p = path.Clean(p)
	if p == "/" || p == "." {
		return p, nil
	}

	parent := path.Dir(p)
	base := path.Base(p)

	rawParent, err := c.resolveDir(ctx, parent)
	if err != nil {
		return "", fmt.Errorf("写入 %q 失败: 无法唯一确定父目录 %q: %w", p, DecodeServerName(parent), err)
	}

	// 写路径不复用索引：必须拿到"当前"的原始文件名，避免用旧索引写错物理条目。
	if isASCIIBytes(base) {
		target := path.Join(rawParent, base)
		if _, serr := c.b.Stat(target); serr == nil {
			return target, nil
		} else if !errors.Is(serr, os.ErrNotExist) {
			return "", fmt.Errorf("写入 %q 失败: %w", p, serr)
		}
		return target, nil
	}

	resolved, cerr := c.resolveChildPath(ctx, rawParent, base, false)
	if cerr == nil {
		return resolved, nil
	}
	if !errors.Is(cerr, os.ErrNotExist) {
		return "", fmt.Errorf("写入 %q 失败: %w", p, cerr)
	}
	encoded, eerr := encodeSegmentForParent(rawParent, base)
	if eerr != nil {
		return "", fmt.Errorf("写入 %q 失败: %w", p, eerr)
	}
	return path.Join(rawParent, encoded), nil
}

// encodeSegmentForParent 按"已解析原始父路径"的编码约定编码一个新段。
//
//   - ASCII 段：UTF-8 与 GB18030 字节一致，直接返回，不做任何转码。
//   - 已经是原始字节（非法 UTF-8）的段：原样返回。
//   - 父路径是非 UTF-8（GBK 盘）→ 新段按 GB18030 编码，落在同一个物理目录。
//   - 父路径是纯 ASCII / UTF-8 → 保持 UTF-8 语义（与既有行为一致）。
//
// 注意：这里只编码"单个新段"，绝不对混合 UTF-8/GBK 的完整绝对路径整体转码。
func encodeSegmentForParent(rawParent, seg string) (string, error) {
	if seg == "" || seg == "." || seg == ".." {
		return "", fmt.Errorf("非法的路径段 %q", seg)
	}
	if strings.ContainsAny(seg, "/\x00\r\n") {
		return "", fmt.Errorf("路径段含非法字符: %q", seg)
	}
	if isASCIIBytes(seg) || !utf8.ValidString(seg) {
		return seg, nil
	}
	if utf8.ValidString(rawParent) {
		return seg, nil
	}
	gbk, _, err := transform.Bytes(simplifiedchinese.GB18030.NewEncoder(), []byte(seg))
	if err != nil || len(gbk) == 0 {
		return "", fmt.Errorf("按 GBK 编码路径段 %q 失败: %w", seg, err)
	}
	return string(gbk), nil
}

// resolveDirectoryForCreate 解析"允许创建"的目录路径（OTH-04）。
//
// 语义（对应审核方案）：
//   - 逐段解析已存在前缀：每段都必须在已解析原始父目录下唯一命中，
//     否则立即终止（歧义 / 权限 / 协议错误都不许继续）；
//   - 第一个"确定不存在"的段：基于已经确定的原始父路径编码该新段，
//     之后的新段沿用同一编码约定继续向下；
//   - 不对混合 UTF-8/GBK 的完整绝对路径整体转码，也不丢弃已经成功解析的前缀。
func (c *Client) resolveDirectoryForCreate(ctx context.Context, dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	if raw, ok := DecodePathIdentity(dir); ok {
		return raw, nil
	}
	cleaned := path.Clean(dir)
	if cleaned == "/" || cleaned == "." {
		return "/", nil
	}
	if err := ctxErr(ctx); err != nil {
		return "", err
	}

	cur := "/"
	creating := false
	for _, seg := range splitPathSegments(cleaned) {
		if err := ctxErr(ctx); err != nil {
			return "", err
		}
		if !creating {
			resolved, err := c.resolveChildPath(ctx, cur, seg, false)
			if err == nil {
				cur = resolved
				continue
			}
			if !errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("解析目录 %q 的段 %q 失败: %w", DecodeServerName(cleaned), DecodeServerName(seg), err)
			}
			creating = true
		}
		encoded, err := encodeSegmentForParent(cur, seg)
		if err != nil {
			return "", err
		}
		cur = path.Join(cur, encoded)
	}
	return cur, nil
}

// ----------------------------------------------------------------------------
// 对外 ctx 变体（handler / 比对适配器用得上，避免昂贵的解析阶段无法取消）
// ----------------------------------------------------------------------------

// StatCtx 带 ctx 的 Stat（解析阶段可取消）。
func (c *Client) StatCtx(ctx context.Context, p string) (os.FileInfo, error) {
	if c == nil || c.b == nil {
		return nil, fmt.Errorf("sftp 客户端未连接")
	}
	resolved, err := c.resolveExistingPathCtx(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("stat 失败: %w", err)
	}
	info, err := c.b.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat 失败: %w", err)
	}
	return wrapDecodedOne(info, resolved), nil
}

// OpenCtx 带 ctx 的 Open（解析阶段可取消）。
func (c *Client) OpenCtx(ctx context.Context, p string) (SftpFile, error) {
	if c == nil || c.b == nil {
		return nil, fmt.Errorf("sftp 客户端未连接")
	}
	resolved, err := c.resolveExistingPathCtx(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("打开远端文件失败: %w", err)
	}
	f, err := c.b.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("打开远端文件失败: %w", err)
	}
	return f, nil
}

// ReadDirCtx 带 ctx 的 ReadDir。
func (c *Client) ReadDirCtx(ctx context.Context, p string) ([]os.FileInfo, error) {
	if c == nil || c.b == nil {
		return nil, fmt.Errorf("sftp 客户端未连接")
	}
	resolved, err := c.resolveExistingPathCtx(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("列出目录失败: %w", err)
	}
	infos, err := c.b.ReadDir(resolved)
	if err != nil {
		return nil, fmt.Errorf("列出目录失败: %w", err)
	}
	return wrapDecoded(infos, resolved), nil
}

// ListLimitedCtx 带 ctx 的 ListLimited。
func (c *Client) ListLimitedCtx(ctx context.Context, p string, max int) ([]os.FileInfo, bool, error) {
	if c == nil || c.b == nil {
		return nil, false, fmt.Errorf("sftp 客户端未连接")
	}
	resolved, err := c.resolveExistingPathCtx(ctx, p)
	if err != nil {
		return nil, false, fmt.Errorf("列出目录失败: %w", err)
	}
	entries, truncated, err := c.b.ListLimited(resolved, max)
	if err != nil {
		return nil, false, fmt.Errorf("列出目录失败: %w", err)
	}
	return wrapDecoded(entries, resolved), truncated, nil
}

// MkdirAllCtx 带 ctx 的多级目录创建（目录创建专用解析，见 resolveDirectoryForCreate）。
func (c *Client) MkdirAllCtx(ctx context.Context, dir string) error {
	if c == nil || c.b == nil {
		return fmt.Errorf("SFTP 客户端未初始化")
	}
	maker, ok := c.b.(interface{ MkdirAll(string) error })
	if !ok {
		return fmt.Errorf("当前 SFTP 后端不支持创建目录")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resolved, err := c.resolveDirectoryForCreate(ctx, dir)
	if err != nil {
		return fmt.Errorf("创建目录 %q 失败: %w", dir, err)
	}
	if err := maker.MkdirAll(resolved); err != nil {
		return err
	}
	c.invalidateDirIndex(path.Dir(resolved))
	return nil
}

// ResolveDirIdentityCtx 把展示目录解析成原始字节绝对路径（handler 用它生成目录身份）。
func (c *Client) ResolveDirIdentityCtx(ctx context.Context, dir string) (string, error) {
	if c == nil || c.b == nil {
		return "", fmt.Errorf("sftp 客户端未连接")
	}
	if raw, ok := DecodePathIdentity(dir); ok {
		return raw, nil
	}
	raw, err := c.resolveDir(ctx, dir)
	if err != nil {
		return "", fmt.Errorf("解析目录身份失败: %w", err)
	}
	return raw, nil
}

// ResolveDirIdentity 见 ResolveDirIdentityCtx。
func (c *Client) ResolveDirIdentity(dir string) (string, error) {
	return c.ResolveDirIdentityCtx(context.Background(), dir)
}
