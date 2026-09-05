package waspack

import (
	"archive/tar"
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ZipResult 是一键打 ZIP 的结果：把 war 当前全部文件装进 <包名>/<相对路径>，
// zip 文件名为 <包名>.zip，放在输出目录下。
type ZipResult struct {
	OK          bool             `json:"ok"`
	OutputDir   string           `json:"output_dir"`
	WarDir      string           `json:"war_dir"`
	ZipFile     string           `json:"zip_file"`
	PackageName string           `json:"package_name"`
	Source      string           `json:"source"`
	Files       int              `json:"files"`
	Bytes       int64            `json:"bytes"`
	Warnings    []string         `json:"warnings,omitempty"`
	Artifacts   []ArtifactDigest `json:"artifacts,omitempty"`
}

// BuildZip 优先把 war 目录下所有文件装进 ZIP；若用户走的是“一键直接打包”，
// 没有 war 目录，则读取同名 tar 生成 ZIP。两条操作路径都能自然衔接第 5 步。
// 例如包名 AP2026qijunV1，则 zip 内为 AP2026qijunV1/xxx，zip 文件为 AP2026qijunV1.zip。
func BuildZip(req Request) (*ZipResult, error) {
	if err := validateReplaceAuthorization(req); err != nil {
		return nil, err
	}
	unlock, err := lockOutputDir(req.OutputDir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return buildZipLocked(req)
}

func buildZipLocked(req Request) (*ZipResult, error) {
	pkgName, err := SanitizePackageName(req.PackageName)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.OutputDir) == "" {
		return nil, fmt.Errorf("请先填写打包目标目录")
	}
	outAbs, err := filepath.Abs(strings.TrimSpace(req.OutputDir))
	if err != nil || !isSafeLocalPath(outAbs) {
		return nil, fmt.Errorf("打包目录无效")
	}
	if _, err := validateOutputAgainstProject(req.ProjectDir, outAbs); err != nil {
		return nil, err
	}
	warDir := filepath.Join(outAbs, ExtractedWARDirName)
	source := "war"
	type zipEntry struct {
		abs string
		rel string // 相对 war 的 slash 路径
		sz  int64
	}
	var entries []zipEntry
	tarPath := filepath.Join(outAbs, TarFileName(pkgName))
	files, expectedBytes := 0, int64(0)
	st, warErr := os.Lstat(warDir)
	if warErr == nil {
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return nil, fmt.Errorf("war 路径不是安全文件夹，拒绝打 ZIP")
		}
		var walkedBytes int64
		err = filepath.Walk(warDir, func(p string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("war 中包含符号链接，拒绝打包: %s", p)
			}
			if info.IsDir() {
				return nil
			}
			if info.Size() < 0 || walkedBytes > MaxArchiveBytes-info.Size() {
				return fmt.Errorf("war 内容超过 %d 字节上限", MaxArchiveBytes)
			}
			if len(entries) >= MaxFiles {
				return fmt.Errorf("war 文件数超过 %d 上限", MaxFiles)
			}
			walkedBytes += info.Size()
			rel, err := filepath.Rel(warDir, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			entries = append(entries, zipEntry{abs: p, rel: rel, sz: info.Size()})
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return nil, fmt.Errorf("war 文件夹为空，请先抽取文件")
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
		files = len(entries)
		for _, entry := range entries {
			expectedBytes += entry.sz
		}
	} else if os.IsNotExist(warErr) {
		source = "tar"
		warDir = ""
		files, expectedBytes, err = scanTarForZip(tarPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("找不到可打包的 war 文件夹或同名 tar，请先执行“2. 一键抽取”或“4. 一键直接打包”")
			}
			return nil, err
		}
	} else {
		return nil, warErr
	}

	stage, err := os.MkdirTemp(filepath.Dir(outAbs), ".kairo-waspack-stage-")
	if err != nil {
		return nil, fmt.Errorf("创建临时 ZIP 目录失败: %w", err)
	}
	defer os.RemoveAll(stage)
	zipPath := filepath.Join(stage, pkgName+".zip")

	f, err := os.OpenFile(zipPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("创建 zip: %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(zipPath)
		}
	}()
	zw := zip.NewWriter(f)
	var total int64
	if source == "tar" {
		total, err = copyTarIntoZip(zw, tarPath, pkgName)
	} else {
		for _, e := range entries {
			zipName := pkgName + "/" + e.rel
			w, createErr := zw.Create(zipName)
			if createErr != nil {
				err = fmt.Errorf("写 zip 条目 %s: %w", zipName, createErr)
				break
			}
			in, openErr := os.Open(e.abs)
			if openErr != nil {
				err = fmt.Errorf("打开 %s: %w", e.rel, openErr)
				break
			}
			n, copyErr := io.Copy(w, in)
			_ = in.Close()
			if copyErr != nil {
				err = fmt.Errorf("写 zip 内容 %s: %w", zipName, copyErr)
				break
			}
			total += n
		}
	}
	if err != nil {
		_ = zw.Close()
		return nil, err
	}
	if total != expectedBytes {
		_ = zw.Close()
		return nil, fmt.Errorf("ZIP 内容字节数校验失败: 预期 %d，实际 %d", expectedBytes, total)
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := publishNamedStagedArtifactsWithMetadata(stage, outAbs, req.OutputPolicy, req.ConfirmReplace, []string{pkgName + ".zip"}, req.MetadataDir); err != nil {
		return nil, err
	}
	zipPath = filepath.Join(outAbs, pkgName+".zip")
	ok = true
	_ = os.Remove(filepath.Join(outAbs, legacyOutputMarkerName))
	warnings := []string(nil)
	if ownershipErr := reconcileOwnedArtifactsAt(outAbs, req.MetadataDir, []string{pkgName + ".zip"}); ownershipErr != nil {
		warnings = append(warnings, "无法写入清理归属元数据："+ownershipErr.Error())
	}
	result := &ZipResult{OK: true, OutputDir: outAbs, WarDir: warDir, ZipFile: zipPath, PackageName: pkgName, Source: source, Files: files, Bytes: total, Warnings: warnings}
	artifacts, digestWarnings := ZipArtifacts(result)
	result.Artifacts = artifacts
	result.Warnings = append(result.Warnings, digestWarnings...)
	return result, nil
}

func safeTarZipRel(name string) (string, error) {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	clean := path.Clean(name)
	isDrivePath := len(clean) >= 2 && clean[1] == ':' && (clean[0] >= 'a' && clean[0] <= 'z' || clean[0] >= 'A' && clean[0] <= 'Z')
	if clean == "." || path.IsAbs(clean) || isDrivePath || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("tar 包含非法路径 %q，拒绝生成 ZIP", name)
	}
	return clean, nil
}

func scanTarForZip(tarPath string) (files int, total int64, err error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		hdr, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return 0, 0, fmt.Errorf("读取 tar 失败: %w", nextErr)
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return 0, 0, fmt.Errorf("tar 包含非普通文件 %q，拒绝生成 ZIP", hdr.Name)
		}
		if _, err := safeTarZipRel(hdr.Name); err != nil {
			return 0, 0, err
		}
		files++
		if files > MaxFiles {
			return 0, 0, fmt.Errorf("tar 文件数超过 %d 上限", MaxFiles)
		}
		if hdr.Size < 0 || total > MaxArchiveBytes-hdr.Size {
			return 0, 0, fmt.Errorf("tar 内容超过 %d 字节上限", MaxArchiveBytes)
		}
		total += hdr.Size
	}
	if files == 0 {
		return 0, 0, fmt.Errorf("同名 tar 为空，无法生成 ZIP")
	}
	return files, total, nil
}

func copyTarIntoZip(zw *zip.Writer, tarPath, pkgName string) (int64, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	var total int64
	files := 0
	for {
		hdr, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return total, fmt.Errorf("读取 tar 失败: %w", nextErr)
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		files++
		if files > MaxFiles {
			return total, fmt.Errorf("tar 文件数超过 %d 上限", MaxFiles)
		}
		rel, relErr := safeTarZipRel(hdr.Name)
		if relErr != nil {
			return total, relErr
		}
		w, createErr := zw.Create(pkgName + "/" + rel)
		if createErr != nil {
			return total, fmt.Errorf("写 zip 条目 %s: %w", rel, createErr)
		}
		n, copyErr := io.Copy(w, tr)
		if copyErr != nil {
			return total, fmt.Errorf("写 zip 内容 %s: %w", rel, copyErr)
		}
		total += n
		if total > MaxArchiveBytes {
			return total, fmt.Errorf("tar 内容超过 %d 字节上限", MaxArchiveBytes)
		}
	}
	return total, nil
}
