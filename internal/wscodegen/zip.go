package wscodegen

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"kairo/internal/webservice"
)

const maxZipBytes = 64 << 20

// ZipFiles 把内存里的生成结果打成 zip，路径用 RelPath（正斜杠）。
func ZipFiles(files []GeneratedFile) ([]byte, error) {
	if len(files) == 0 {
		return nil, errors.New("没有可打包的文件")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	seen := map[string]struct{}{}
	var total int
	for _, f := range files {
		rel, err := safeZipPath(f.RelPath)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		candidate := rel
		if _, exists := seen[candidate]; exists {
			ext := filepath.Ext(rel)
			base := strings.TrimSuffix(rel, ext)
			counter := 2
			for {
				candidate = fmt.Sprintf("%s_%d%s", base, counter, ext)
				if _, exists := seen[candidate]; !exists {
					break
				}
				counter++
			}
		}
		seen[candidate] = struct{}{}
		header := &zip.FileHeader{
			Name:   candidate,
			Method: zip.Deflate,
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			_ = zw.Close()
			return nil, fmt.Errorf("写入 zip 头失败: %w", err)
		}
		n, err := io.WriteString(w, f.Content)
		if err != nil {
			_ = zw.Close()
			return nil, fmt.Errorf("写入 zip 内容失败: %w", err)
		}
		total += n
		if total > maxZipBytes {
			_ = zw.Close()
			return nil, fmt.Errorf("生成结果超过 %d MB，拒绝打包", maxZipBytes>>20)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("关闭 zip 失败: %w", err)
	}
	return buf.Bytes(), nil
}

// ZipGeneratedTree 把官方工具写出的源码树打成 zip（只收 java/txt/xml/wsdd）。
func ZipGeneratedTree(root string) ([]byte, int, error) {
	var files []GeneratedFile
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return err
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if ext != ".java" && ext != ".txt" && ext != ".xml" && ext != ".wsdd" {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		files = append(files, GeneratedFile{
			RelPath: filepath.ToSlash(rel),
			Content: string(data),
			Kind:    strings.TrimPrefix(ext, "."),
		})
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	data, err := ZipFiles(files)
	return data, len(files), err
}

// ZipFileName 给浏览器下载用的文件名，只保留安全 ASCII。
func ZipFileName(res Result) string {
	base := "wscodegen"
	if pkg := strings.TrimSpace(res.PackageName); pkg != "" {
		parts := strings.Split(pkg, ".")
		if last := parts[len(parts)-1]; last != "" {
			base = last
		}
	}
	eng := strings.TrimSpace(res.Engine)
	if eng == "" {
		eng = "java"
	}
	return sanitizeZipName(base + "-" + eng + ".zip")
}

// PrepareZip 生成完整源码并打包。内置模式走内存；官方工具写到临时目录再收。
func PrepareZip(ctx context.Context, req Request, store *webservice.Store) (string, []byte, int, Result, error) {
	req.OpenAfter = false
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = ModeBuiltin
	}
	if mode == ModeTool {
		tmp, err := os.MkdirTemp("", "kairo-wscodegen-zip-")
		if err != nil {
			return "", nil, 0, Result{}, err
		}
		defer os.RemoveAll(tmp)
		req.OutputDir = tmp
		req.DryRun = false
		res, err := GenerateContext(ctx, req, store)
		if err != nil {
			return "", nil, 0, res, err
		}
		data, n, err := ZipGeneratedTree(tmp)
		if err != nil {
			return "", nil, 0, res, err
		}
		return ZipFileName(res), data, n, res, nil
	}
	req.DryRun = true
	req.OutputDir = ""
	res, err := GenerateContext(ctx, req, store)
	if err != nil {
		return "", nil, 0, res, err
	}
	data, err := ZipFiles(res.Files)
	if err != nil {
		return "", nil, 0, res, err
	}
	return ZipFileName(res), data, len(res.Files), res, nil
}

func safeZipPath(rel string) (string, error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || strings.Contains(rel, "..") || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("非法输出路径: %s", rel)
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("非法输出路径: %s", rel)
		}
	}
	return rel, nil
}

func sanitizeZipName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if r > unicode.MaxASCII {
				b.WriteByte('_')
				continue
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" || strings.EqualFold(out, "zip") {
		return "wscodegen.zip"
	}
	if !strings.HasSuffix(strings.ToLower(out), ".zip") {
		out += ".zip"
	}
	return out
}
