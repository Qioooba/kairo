package httpserver

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"ops-toolbox/internal/diff"
)

type folderScanReq struct {
	LeftPath  string `json:"left_path"`
	RightPath string `json:"right_path"`
}

type fileEntry struct {
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"is_dir"`
	Hash    string `json:"hash,omitempty"`
	Status  string `json:"status"`
}

type folderScanResp struct {
	Left struct {
		Root string      `json:"root"`
		Tree []fileEntry `json:"tree"`
	} `json:"left"`
	Right struct {
		Root string      `json:"root"`
		Tree []fileEntry `json:"tree"`
	} `json:"right"`
	Diff struct {
		Same      []string `json:"same"`
		Different []string `json:"different"`
		LeftOnly  []string `json:"left_only"`
		RightOnly []string `json:"right_only"`
	} `json:"diff"`
	// Truncated 任一侧目录文件数超过 maxFiles 上限被截断时为 true。
	Truncated bool `json:"truncated"`
}

type fileDiffReq struct {
	LeftPath  string `json:"left_path"`
	RightPath string `json:"right_path"`
}

type fileDiffResp struct {
	Unified string `json:"unified"`
}

var ignoreScanDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	"target":       true,
	"dist":         true,
	"build":        true,
	".idea":        true,
	".vscode":      true,
	".next":        true,
	".nuxt":        true,
}

// maxFileSize 单文件大小上限：超过时跳过 hashFile 计算避免占内存。
const maxFileSize = 100 * 1024 * 1024 // 100MB

func shouldIgnoreScanPath(relPath string) bool {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for _, p := range parts {
		if ignoreScanDirs[p] {
			return true
		}
	}
	return false
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// scanDir 递归遍历 root 收集文件/目录条目。第二个返回值 truncated 为 true 表示
// 文件数超过 maxFiles 上限被截断（避免 10 万文件目录阻塞 worker、占满内存）。
func scanDir(root string) (entries map[string]fileEntry, truncated bool, err error) {
	entries = make(map[string]fileEntry)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, false, err
	}
	stat, err := os.Stat(absRoot)
	if err != nil {
		return nil, false, err
	}
	if !stat.IsDir() {
		return nil, false, fmt.Errorf("路径不是目录: %s", root)
	}

	// maxFiles 限制单侧目录收集的文件数上限，超过后停止遍历并标记 truncated。
	const maxFiles = 10000
	fileCount := 0
	walkErr := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		relPath, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		if relPath == "." {
			return nil
		}
		relSlash := filepath.ToSlash(relPath)
		if d.IsDir() {
			if shouldIgnoreScanPath(relSlash) {
				return filepath.SkipDir
			}
			entries[relSlash] = fileEntry{
				Path:    path,
				RelPath: relSlash,
				Size:    0,
				IsDir:   true,
				Status:  "same",
			}
			return nil
		}
		if shouldIgnoreScanPath(relSlash) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries[relSlash] = fileEntry{
			Path:    path,
			RelPath: relSlash,
			Size:    info.Size(),
			IsDir:   false,
			Status:  "same",
		}
		fileCount++
		if fileCount >= maxFiles {
			truncated = true
			return io.EOF
		}
		return nil
	})
	// io.EOF 是我们主动用来终止遍历的，不是真实错误。
	if walkErr != nil && !errors.Is(walkErr, io.EOF) {
		return nil, false, walkErr
	}
	return entries, truncated, nil
}

func (s *Server) handleCompareFolderScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req folderScanReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	// BE-017：补审计日志（/api/compare/* 之前是审计盲区）。
	s.audit.Write("compare.folder_scan", "left", req.LeftPath, "right", req.RightPath)
	req.LeftPath = strings.TrimSpace(req.LeftPath)
	req.RightPath = strings.TrimSpace(req.RightPath)
	if req.LeftPath == "" || req.RightPath == "" {
		writeErr(w, 400, errors.New("left_path 和 right_path 不能为空"))
		return
	}

	// BE-001 fail-closed：路径必须在 app.compare_allowed_roots 白名单里。
	// 空 roots → 一律 403（防止任意文件读）。
	cur := s.cfg.Get()
	if !cur.App.ComparePathAllowed(req.LeftPath) || !cur.App.ComparePathAllowed(req.RightPath) {
		writeErr(w, 403, errors.New("路径不在 compare_allowed_roots 白名单内（fail-closed）"))
		return
	}

	leftEntries, leftTrunc, err := scanDir(req.LeftPath)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("扫描左侧目录失败: %w", err))
		return
	}
	rightEntries, rightTrunc, err := scanDir(req.RightPath)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("扫描右侧目录失败: %w", err))
		return
	}

	leftAbs, _ := filepath.Abs(req.LeftPath)
	rightAbs, _ := filepath.Abs(req.RightPath)

	allPaths := make(map[string]bool)
	for p := range leftEntries {
		allPaths[p] = true
	}
	for p := range rightEntries {
		allPaths[p] = true
	}

	// 注意：same/different/leftOnly/rightOnly 必须初始化为非 nil 的空 slice，
	// 否则没有差异时会被 encoding/json 序列化成 null，导致前端 .length 访问崩溃
	// （典型场景：左右填同一目录做"基准校验"，所有字段都为空）。
	var leftTree, rightTree []fileEntry
	same := []string{}
	different := []string{}
	leftOnly := []string{}
	rightOnly := []string{}

	for relPath := range allPaths {
		left, hasLeft := leftEntries[relPath]
		right, hasRight := rightEntries[relPath]

		if hasLeft && !hasRight {
			left.Status = "left_only"
			leftTree = append(leftTree, left)
			if !left.IsDir {
				leftOnly = append(leftOnly, relPath)
			}
		} else if !hasLeft && hasRight {
			right.Status = "right_only"
			rightTree = append(rightTree, right)
			if !right.IsDir {
				rightOnly = append(rightOnly, relPath)
			}
		} else {
			status := "same"
			if left.IsDir != right.IsDir {
				status = "different"
			} else if !left.IsDir && !right.IsDir {
				if left.Size != right.Size {
					status = "different"
				} else if left.Size > maxFileSize || right.Size > maxFileSize {
					// 单文件过大（>100MB）时跳过 hashFile 计算，避免读整个文件占内存。
					// 大小相同但无法校验哈希，保守标为 different。
					status = "different"
				} else {
					leftHash, err1 := hashFile(left.Path)
					rightHash, err2 := hashFile(right.Path)
					if err1 != nil || err2 != nil || leftHash != rightHash {
						status = "different"
					}
				}
			}
			left.Status = status
			right.Status = status
			leftTree = append(leftTree, left)
			rightTree = append(rightTree, right)
			if !left.IsDir {
				if status == "same" {
					same = append(same, relPath)
				} else {
					different = append(different, relPath)
				}
			}
		}
	}

	resp := folderScanResp{}
	resp.Left.Root = leftAbs
	resp.Right.Root = rightAbs
	resp.Left.Tree = leftTree
	resp.Right.Tree = rightTree
	resp.Diff.Same = same
	resp.Diff.Different = different
	resp.Diff.LeftOnly = leftOnly
	resp.Diff.RightOnly = rightOnly
	resp.Truncated = leftTrunc || rightTrunc

	writeJSON(w, 200, resp)
}

func (s *Server) handleCompareFileDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req fileDiffReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	// BE-017：补审计日志（/api/compare/* 之前是审计盲区）。
	s.audit.Write("compare.file_diff", "left", req.LeftPath, "right", req.RightPath)
	req.LeftPath = strings.TrimSpace(req.LeftPath)
	req.RightPath = strings.TrimSpace(req.RightPath)
	if req.LeftPath == "" || req.RightPath == "" {
		writeErr(w, 400, errors.New("left_path 和 right_path 不能为空"))
		return
	}

	// BE-001 fail-closed：路径必须在 app.compare_allowed_roots 白名单里。
	// 空 roots → 一律 403（防止任意文件读）。
	cur := s.cfg.Get()
	if !cur.App.ComparePathAllowed(req.LeftPath) || !cur.App.ComparePathAllowed(req.RightPath) {
		writeErr(w, 403, errors.New("路径不在 compare_allowed_roots 白名单内（fail-closed）"))
		return
	}

	// 单文件 4MB 限制和文本比对保持一致；用 internal/diff.Compare 而非 exec `diff`
	// 是为了跨平台（Windows 没有系统 diff）以及和 /api/diff/compare 行为对齐。
	const maxBytes = 4 * 1024 * 1024
	leftBytes, err := os.ReadFile(req.LeftPath)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("读取左侧文件失败: %w", err))
		return
	}
	rightBytes, err := os.ReadFile(req.RightPath)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("读取右侧文件失败: %w", err))
		return
	}
	if len(leftBytes) > maxBytes {
		writeErr(w, 400, fmt.Errorf("左侧文件超过 4MB 上限"))
		return
	}
	if len(rightBytes) > maxBytes {
		writeErr(w, 400, fmt.Errorf("右侧文件超过 4MB 上限"))
		return
	}

	leftLines := strings.Split(strings.ReplaceAll(string(leftBytes), "\r\n", "\n"), "\n")
	rightLines := strings.Split(strings.ReplaceAll(string(rightBytes), "\r\n", "\n"), "\n")

	res := diff.Compare(leftLines, rightLines, req.LeftPath, req.RightPath)
	writeJSON(w, 200, fileDiffResp{Unified: res.UnifiedDiff})
}
