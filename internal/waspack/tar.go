package waspack

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func writeTar(dest string, files []ResolvedFile, isBatch ...bool) (int64, error) {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("创建 tar: %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(dest)
		}
	}()

	tw := tar.NewWriter(f)
	defer tw.Close()

	batchMode := len(isBatch) > 0 && isBatch[0]
	var written int64
	for _, rf := range files {
		n, err := addTarFile(tw, rf, batchMode)
		if err != nil {
			return 0, err
		}
		written += n
	}
	if err := tw.Close(); err != nil {
		return 0, err
	}
	ok = true
	return written, nil
}

func addTarFile(tw *tar.Writer, rf ResolvedFile, isBatch bool) (int64, error) {
	src, err := os.Open(rf.Abs)
	if err != nil {
		return 0, fmt.Errorf("打开 %s: %w", rf.Rel, err)
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return 0, err
	}
	hdr, err := tar.FileInfoHeader(st, "")
	if err != nil {
		return 0, err
	}
	cleanRel := strings.TrimPrefix(rf.Rel, "./")
	cleanRel = strings.ReplaceAll(cleanRel, "\\", "/")
	if isBatch {
		// 批量打包：压缩包内第一层目录直接为清单目录（如 amargci、AmarExtract）
		hdr.Name = cleanRel
	} else {
		// 应用代码打包：保留 ./ 路径前缀与银行历史脚本规范对齐
		if !strings.HasPrefix(cleanRel, "./") {
			hdr.Name = "./" + cleanRel
		} else {
			hdr.Name = cleanRel
		}
	}
	hdr.Format = tar.FormatGNU
	hdr.Uid = 0
	hdr.Gid = 0
	hdr.Uname = ""
	hdr.Gname = ""
	if hdr.ModTime.IsZero() {
		hdr.ModTime = time.Now()
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return 0, fmt.Errorf("写 tar 头 %s: %w", hdr.Name, err)
	}
	n, err := io.Copy(tw, src)
	if err != nil {
		return 0, fmt.Errorf("写 tar 内容 %s: %w", hdr.Name, err)
	}
	return n, nil
}
