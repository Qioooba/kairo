package httpserver

import (
	"context"
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
	"time"

	"kairo/internal/comparefs"
	"kairo/internal/config"
	"kairo/internal/diff"
)

type folderScanReq struct {
	LeftPath   string   `json:"left_path"`
	RightPath  string   `json:"right_path"`
	DeepCheck  bool     `json:"deep_check,omitempty"`
	IgnoreExts []string `json:"ignore_exts,omitempty"`
	MinSize    int64    `json:"min_size,omitempty"`
	MaxSize    int64    `json:"max_size,omitempty"`
}

type fileEntry struct {
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
	IsDir   bool   `json:"is_dir"`
	Hash    string `json:"hash,omitempty"`
	Status  string `json:"status"`
	Checked bool   `json:"checked,omitempty"`
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
		Suspect   []string `json:"suspect"`
		LeftOnly  []string `json:"left_only"`
		RightOnly []string `json:"right_only"`
	} `json:"diff"`
	Truncated bool  `json:"truncated"`
	DeepMode  bool  `json:"deep_mode"`
	ScanMs    int64 `json:"scan_ms"`
}

type deepCheckReq struct {
	Files []struct {
		LeftPath  string `json:"left_path"`
		RightPath string `json:"right_path"`
		RelPath   string `json:"rel_path"`
	} `json:"files"`
}

type deepCheckResp struct {
	Results map[string]string `json:"results"`
	Ms      int64             `json:"ms"`
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

const maxFileSize = 100 * 1024 * 1024
const maxHashFileSize = 50 * 1024 * 1024

func shouldIgnoreScanPath(relPath string) bool {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for _, p := range parts {
		if ignoreScanDirs[p] {
			return true
		}
	}
	return false
}

func shouldIgnoreByExt(relPath string, ignoreExts []string) bool {
	if len(ignoreExts) == 0 {
		return false
	}
	ext := strings.ToLower(filepath.Ext(relPath))
	for _, ie := range ignoreExts {
		ie = strings.ToLower(strings.TrimSpace(ie))
		if ie == "" {
			continue
		}
		if !strings.HasPrefix(ie, ".") {
			ie = "." + ie
		}
		if ext == ie {
			return true
		}
	}
	return false
}

func hashFile(path string, roots ...string) (string, error) {
	f, err := comparefs.NewLocalWithAllowedRoots(roots).Open(context.Background(), path)
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

func quickHashSizeMtime(size int64, mtime time.Time) string {
	return fmt.Sprintf("%d-%d", size, mtime.UnixNano())
}

func scanDir(root string, opts folderScanReq) (entries map[string]fileEntry, truncated bool, err error) {
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

	const maxFiles = 50000
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
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if shouldIgnoreScanPath(relSlash) {
				return filepath.SkipDir
			}
			entries[relSlash] = fileEntry{
				Path:    path,
				RelPath: relSlash,
				Size:    0,
				ModTime: 0,
				IsDir:   true,
				Status:  "same",
			}
			return nil
		}
		if shouldIgnoreScanPath(relSlash) {
			return nil
		}
		if shouldIgnoreByExt(relSlash, opts.IgnoreExts) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if opts.MinSize > 0 && info.Size() < opts.MinSize {
			return nil
		}
		if opts.MaxSize > 0 && info.Size() > opts.MaxSize {
			return nil
		}
		entries[relSlash] = fileEntry{
			Path:    path,
			RelPath: relSlash,
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
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
	start := time.Now()
	var req folderScanReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	s.audit.Write("compare.folder_scan", "left", req.LeftPath, "right", req.RightPath, "deep", req.DeepCheck)
	req.LeftPath = strings.TrimSpace(req.LeftPath)
	req.RightPath = strings.TrimSpace(req.RightPath)
	if req.LeftPath == "" || req.RightPath == "" {
		writeErr(w, 400, errors.New("left_path 和 right_path 不能为空"))
		return
	}

	cur := s.cfg.Get()
	if !legacyComparePathAllowed(&cur.App, req.LeftPath) || !legacyComparePathAllowed(&cur.App, req.RightPath) {
		writeErr(w, 403, errors.New("路径不在 compare_allowed_roots 白名单内"))
		return
	}

	leftEntries, leftTrunc, err := scanDir(req.LeftPath, req)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("扫描左侧目录失败: %w", err))
		return
	}
	rightEntries, rightTrunc, err := scanDir(req.RightPath, req)
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

	var leftTree, rightTree []fileEntry
	same := []string{}
	different := []string{}
	suspect := []string{}
	leftOnly := []string{}
	rightOnly := []string{}

	sortedPaths := make([]string, 0, len(allPaths))
	for p := range allPaths {
		sortedPaths = append(sortedPaths, p)
	}

	for _, relPath := range sortedPaths {
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
				} else if req.DeepCheck {
					if left.Size > maxHashFileSize || right.Size > maxHashFileSize {
						status = "suspect"
					} else {
						leftHash, err1 := hashFile(left.Path, cur.App.CompareAllowedRoots...)
						rightHash, err2 := hashFile(right.Path, cur.App.CompareAllowedRoots...)
						if err1 != nil || err2 != nil || leftHash != rightHash {
							status = "different"
						} else {
							status = "same"
						}
					}
				} else {
					if left.ModTime != right.ModTime {
						status = "suspect"
					} else {
						status = "same"
					}
				}
			}
			left.Status = status
			right.Status = status
			left.Checked = req.DeepCheck
			right.Checked = req.DeepCheck
			leftTree = append(leftTree, left)
			rightTree = append(rightTree, right)
			if !left.IsDir {
				switch status {
				case "same":
					same = append(same, relPath)
				case "different":
					different = append(different, relPath)
				case "suspect":
					suspect = append(suspect, relPath)
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
	resp.Diff.Suspect = suspect
	resp.Diff.LeftOnly = leftOnly
	resp.Diff.RightOnly = rightOnly
	resp.Truncated = leftTrunc || rightTrunc
	resp.DeepMode = req.DeepCheck
	resp.ScanMs = time.Since(start).Milliseconds()

	writeJSON(w, 200, resp)
}

func (s *Server) handleCompareDeepCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	start := time.Now()
	var req deepCheckReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 10*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	s.audit.Write("compare.deep_check", "count", len(req.Files))

	cur := s.cfg.Get()
	results := make(map[string]string)

	for _, f := range req.Files {
		if !legacyComparePathAllowed(&cur.App, f.LeftPath) || !legacyComparePathAllowed(&cur.App, f.RightPath) {
			results[f.RelPath] = "denied"
			continue
		}
		leftInfo, err1 := os.Stat(f.LeftPath)
		rightInfo, err2 := os.Stat(f.RightPath)
		if err1 != nil || err2 != nil {
			results[f.RelPath] = "different"
			continue
		}
		if leftInfo.IsDir() || rightInfo.IsDir() {
			results[f.RelPath] = "different"
			continue
		}
		if leftInfo.Size() != rightInfo.Size() {
			results[f.RelPath] = "different"
			continue
		}
		if leftInfo.Size() > maxHashFileSize || rightInfo.Size() > maxHashFileSize {
			results[f.RelPath] = "suspect"
			continue
		}
		leftHash, err1 := hashFile(f.LeftPath, cur.App.CompareAllowedRoots...)
		rightHash, err2 := hashFile(f.RightPath, cur.App.CompareAllowedRoots...)
		if err1 != nil || err2 != nil || leftHash != rightHash {
			results[f.RelPath] = "different"
		} else {
			results[f.RelPath] = "same"
		}
	}

	writeJSON(w, 200, deepCheckResp{
		Results: results,
		Ms:      time.Since(start).Milliseconds(),
	})
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
	s.audit.Write("compare.file_diff", "left", req.LeftPath, "right", req.RightPath)
	req.LeftPath = strings.TrimSpace(req.LeftPath)
	req.RightPath = strings.TrimSpace(req.RightPath)
	if req.LeftPath == "" || req.RightPath == "" {
		writeErr(w, 400, errors.New("left_path 和 right_path 不能为空"))
		return
	}

	cur := s.cfg.Get()
	if !legacyComparePathAllowed(&cur.App, req.LeftPath) || !legacyComparePathAllowed(&cur.App, req.RightPath) {
		writeErr(w, 403, errors.New("路径不在 compare_allowed_roots 白名单内"))
		return
	}

	const maxBytes = 4 * 1024 * 1024
	leftBytes, err := readLegacyCompareFile(req.LeftPath, maxBytes, cur.App.CompareAllowedRoots...)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("读取左侧文件失败: %w", err))
		return
	}
	rightBytes, err := readLegacyCompareFile(req.RightPath, maxBytes, cur.App.CompareAllowedRoots...)
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

func legacyComparePathAllowed(app *config.AppConfig, name string) bool {
	if !app.ComparePathAllowed(name) {
		return false
	}
	actual, err := filepath.EvalSymlinks(name)
	return err == nil && app.ComparePathAllowed(actual)
}
func readLegacyCompareFile(name string, limit int64, roots ...string) ([]byte, error) {
	f, err := comparefs.NewLocalWithAllowedRoots(roots).Open(context.Background(), name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit+1))
}
