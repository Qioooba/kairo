package httpserver

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kairo/internal/config"
	"kairo/internal/iconextract"
)

// ---------- 图标缓存 ----------
//
// 设计：
//   - 提取的 exe 图标缓存到 data/opener-icons/{md5(path)}.png
//   - 文件名用 path 的 md5 而非 name，避免改名后命中旧图标
//   - 一个 path 只提取一次：再次保存 opener 时若 path 没变直接复用缓存文件
//   - 前端通过 /api/local/opener-icon?name=xxx 读取，handler 内部 FindOpener → 取 path → 算 md5 → 读文件
//   - 不在 config.yaml 里存 base64（避免配置文件膨胀 + 序列化对比复杂化）

const openerIconDir = "opener-icons"

// openerIconMu 保护 extractAndCacheOpenerIcon 的并发提取。
// SHGetFileInfo + GetDIBits 内部用 GDI，多 goroutine 同时调可能出问题。
var openerIconMu sync.Mutex

// extractAndCacheOpenerIcon 提取 exe 图标并缓存到 data/opener-icons/{md5(path)}.png。
//
//   - 已存在缓存文件且 exe ModTime 未变 → 跳过提取
//   - 提取失败（如非 Windows 平台、文件无图标）→ 不报错，静默跳过（前端 fallback SVG）
//   - 调用方：PUT /api/admin/openers 后台遍历每个 opener 调一次
func (s *Server) extractAndCacheOpenerIcon(op *config.ExternalOpener) {
	if op == nil || strings.TrimSpace(op.Path) == "" {
		return
	}

	iconDir := filepath.Join(s.cur().DataDir(), openerIconDir)
	if err := os.MkdirAll(iconDir, 0o755); err != nil {
		return
	}

	cacheKey := md5Hex(op.Path)
	cachePath := filepath.Join(iconDir, cacheKey+".png")

	// exe ModTime 没变 + 缓存文件存在 → 跳过
	if fi, err := os.Stat(op.Path); err == nil {
		if cached, err := os.Stat(cachePath); err == nil && !fi.ModTime().After(cached.ModTime()) {
			return // 缓存命中
		}
	}

	openerIconMu.Lock()
	defer openerIconMu.Unlock()

	// 拿到锁后再检查一次（可能其他 goroutine 已经提完）
	if _, err := os.Stat(cachePath); err == nil {
		if fi, err := os.Stat(op.Path); err == nil {
			if cached, err := os.Stat(cachePath); err == nil && !fi.ModTime().After(cached.ModTime()) {
				return
			}
		}
	}

	pngBytes, err := iconextract.ExtractPNG(op.Path)
	if err != nil {
		// 非 Windows 平台 / 文件无图标 / GDI 失败 → 静默跳过，前端 fallback
		return
	}

	// 原子写：先写临时文件再 rename，避免读到半截文件
	tmp := cachePath + ".tmp"
	if err := os.WriteFile(tmp, pngBytes, 0o644); err != nil {
		os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, cachePath); err != nil {
		// Windows 上可能因 antivirus/备份软件锁定 rename 失败，退避重试
		// （参考项目 rename 重试约定：短延迟 + 重试，覆盖几十毫秒级锁）
		for i := 0; i < 3; i++ {
			time.Sleep(50 * time.Millisecond)
			if err = os.Rename(tmp, cachePath); err == nil {
				break
			}
		}
		if err != nil {
			os.Remove(tmp)
			return
		}
	}
}

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// refreshOpenerIcons 批量提取并缓存 opener 图标。
// 异步调用（在 PUT /api/admin/openers 之后），不阻塞 HTTP 响应。
// 单个失败不影响其他 opener。
func (s *Server) refreshOpenerIcons(openers []config.ExternalOpener) {
	for i := range openers {
		op := &openers[i]
		s.extractAndCacheOpenerIcon(op)
	}
}

// openerIconCachePath 按 opener name 查找对应的缓存 PNG 路径。
// 返回 (path, exists)；不存在时 exists=false。
func (s *Server) openerIconCachePath(name string) (string, bool) {
	op := s.cur().App.FindOpener(name)
	if op == nil || strings.TrimSpace(op.Path) == "" {
		return "", false
	}
	cacheKey := md5Hex(op.Path)
	p := filepath.Join(s.cur().DataDir(), openerIconDir, cacheKey+".png")
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// ---------- /api/admin/openers/extract-icon ----------
//
// 配置页"选择 exe 后实时预览图标"用。
//
// POST body: { "path": "C:\\Program Files\\Notepad++\\notepad++.exe" }
// 200: { "png_base64": "iVBOR..." }
// 400: path 为空
// 500: 提取失败（非 Windows 平台 / 文件不存在 / 无图标）
//
// 不缓存（配置页可能频繁切换 exe 探测，缓存应在 PUT 时统一做）；
// 平台不支持时返回 200 + png_base64 空字符串，前端走 SVG fallback，不报错。
type openerExtractReq struct {
	Path string `json:"path"`
}

func (s *Server) handleAdminOpenersExtractIcon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req openerExtractReq
	if err := decodeJSONBody(w, r, &req, 8*1024); err != nil {
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeErr(w, 400, errors.New("path 不能为空"))
		return
	}

	pngBytes, err := iconextract.ExtractPNG(req.Path)
	if err != nil {
		// 平台不支持 / 文件无图标 → 返回 200 + 空 base64，前端 fallback
		if errors.Is(err, iconextract.ErrUnsupported) {
			writeJSON(w, 200, map[string]any{
				"png_base64": "",
				"unsupported": true,
				"reason": err.Error(),
			})
			return
		}
		// 其他错误（文件不存在 / GDI 失败）也走 fallback，不弹错误
		writeJSON(w, 200, map[string]any{
			"png_base64": "",
			"unsupported": true,
			"reason": err.Error(),
		})
		return
	}

	writeJSON(w, 200, map[string]any{
		"png_base64": base64.StdEncoding.EncodeToString(pngBytes),
	})
}

// decodeJSONBody 简易 JSON 解码 + limit + 错误响应。
// 跟 handlers_local.go 重复，但为避免改动其他文件，这里 inline 一份。
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) error {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes)).Decode(v); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return err
	}
	return nil
}

// ---------- /api/local/opener-icon?name=xxx ----------
//
// 返回 opener 缓存的 PNG 图标。
//
// 设计：
//   - GET（不用 POST，方便 <img src=...> 直接引用）
//   - 用 If-None-Match + ETag 支持 304
//   - 找不到图标返回 404（前端 <img onerror> 走 SVG fallback）
//   - Content-Type: image/png，Cache-Control 让浏览器缓存一段时间
func (s *Server) handleLocalOpenerIcon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeErr(w, 400, errors.New("name 参数不能为空"))
		return
	}

	cachePath, ok := s.openerIconCachePath(name)
	if !ok {
		writeErr(w, 404, errors.New("图标未缓存（可能非 Windows 平台或提取失败）"))
		return
	}

	// 用 ModTime 当 ETag，简单可靠
	fi, err := os.Stat(cachePath)
	if err != nil {
		writeErr(w, 404, errors.New("图标文件不存在"))
		return
	}
	etag := fmt.Sprintf("\"%x\"", fi.ModTime().UnixNano())
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	f, err := os.Open(cachePath)
	if err != nil {
		writeErr(w, 500, errors.New("打开图标文件失败"))
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size()))
	io.Copy(w, f)
}
