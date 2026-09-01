package waspack

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const ExtractedWARDirName = "war"

// ExtractResult 是第一步“一键抽取”的结果。所有文件先落到目标目录/war，用户可在
// 本地核对或修改，再显式执行第二步打包。
type ExtractResult struct {
	OK          bool     `json:"ok"`
	OutputDir   string   `json:"output_dir"`
	WarDir      string   `json:"war_dir"`
	Files       int      `json:"files"`
	Bytes       int64    `json:"bytes"`
	Warnings    []string `json:"warnings,omitempty"`
	PairedAdded int      `json:"paired_added"`
}

// Extract 只抽取，不创建 tar / list / shell 脚本。目标目录必须为空，避免把旧文件
// 混入新投产包。$1/$2 等内部类已经在 Resolve 的 autoPair 阶段一并收集。
func Extract(req Request) (*ExtractResult, error) {
	pv, err := PreviewRequest(req)
	if err != nil {
		return nil, err
	}
	if len(pv.Missing) > 0 {
		return nil, missingFilesError(pv.Missing)
	}
	outAbs, created, err := prepareOutputDir(req.OutputDir)
	if err != nil {
		return nil, err
	}
	warDir := filepath.Join(outAbs, ExtractedWARDirName)
	if err := os.Mkdir(warDir, 0o755); err != nil {
		if created {
			_ = os.Remove(outAbs)
		}
		return nil, fmt.Errorf("创建 war 抽取目录失败: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(warDir)
			if created {
				_ = os.Remove(outAbs)
			}
		}
	}()
	var total int64
	for _, rf := range pv.Files {
		rel := filepath.FromSlash(strings.TrimPrefix(rf.Rel, "./"))
		dst := filepath.Join(warDir, rel)
		if err := copyExtractedFile(rf.Abs, dst); err != nil {
			return nil, fmt.Errorf("抽取 %s 失败: %w", rf.Rel, err)
		}
		total += rf.Bytes
	}
	ok = true
	return &ExtractResult{OK: true, OutputDir: outAbs, WarDir: warDir, Files: len(pv.Files), Bytes: total, Warnings: pv.Warnings, PairedAdded: pairedCount(pv.Files)}, nil
}

func copyExtractedFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(dst)
		}
	}()
	if _, err := io.CopyBuffer(out, in, make([]byte, 256*1024)); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(dst, st.ModTime(), st.ModTime()); err != nil {
		return err
	}
	ok = true
	return nil
}

// PackageExtracted 是第二步“打包”。它以 war 目录当前内容为准，因此用户在抽取后
// 做的本地修改会进入包；tar 内路径仍与旧的一步打包完全一致。
func PackageExtracted(req Request) (*Result, error) {
	pkgName, err := SanitizePackageName(req.PackageName)
	if err != nil {
		return nil, err
	}
	outAbs, err := filepath.Abs(strings.TrimSpace(req.OutputDir))
	if err != nil || !isSafeLocalPath(outAbs) {
		return nil, fmt.Errorf("打包目录无效")
	}
	warDir := filepath.Join(outAbs, ExtractedWARDirName)
	st, err := os.Lstat(warDir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("找不到可打包的 war 文件夹，请先执行“一键抽取”")
	}
	files, err := filesFromWAR(warDir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("war 文件夹为空，请先抽取文件")
	}
	listPath := filepath.Join(outAbs, ListFileName)
	tarPath := filepath.Join(outAbs, TarFileName(pkgName))
	backupPath := filepath.Join(outAbs, BackupScriptName(pkgName))
	execPath := filepath.Join(outAbs, ExecuteScriptName(pkgName))
	for _, p := range []string{listPath, tarPath, backupPath, execPath} {
		if _, err := os.Lstat(p); err == nil {
			return nil, fmt.Errorf("目标文件已存在，拒绝覆盖: %s", p)
		}
	}
	cleanup := func() { rollback(outAbs, false, listPath, tarPath, backupPath, execPath) }
	if err := writeUnixFile(listPath, listText(files), 0o644); err != nil {
		cleanup()
		return nil, fmt.Errorf("写清单失败: %w", err)
	}
	bytes, err := writeTar(tarPath, files)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := writeUnixFile(backupPath, renderBackupScript(pkgName, files), 0o755); err != nil {
		cleanup()
		return nil, fmt.Errorf("写备份脚本失败: %w", err)
	}
	if err := writeUnixFile(execPath, renderExecuteScript(pkgName, files), 0o755); err != nil {
		cleanup()
		return nil, fmt.Errorf("写执行脚本失败: %w", err)
	}
	return &Result{OK: true, OutputDir: outAbs, WarDir: warDir, TarFile: tarPath, ListFile: listPath, BackupScript: backupPath, ExecuteScript: execPath, PackageName: pkgName, Files: len(files), Bytes: bytes}, nil
}

func filesFromWAR(warDir string) ([]ResolvedFile, error) {
	var files []ResolvedFile
	err := filepath.Walk(warDir, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("war 中包含符号链接，拒绝打包: %s", p)
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(warDir, p)
		if err != nil {
			return err
		}
		rel = "./" + filepath.ToSlash(rel)
		files = append(files, ResolvedFile{Rel: rel, Abs: p, Kind: kindOf(rel), Source: "war", Bytes: info.Size(), Exists: true})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return files, nil
}

func missingFilesError(missing []ResolvedFile) error {
	names := make([]string, 0, min(len(missing), 9))
	for i, m := range missing {
		if i >= 8 {
			names = append(names, fmt.Sprintf("…另有 %d 个", len(missing)-8))
			break
		}
		names = append(names, m.Rel)
	}
	return fmt.Errorf("工程里找不到 %d 个文件，请先编译或检查路径: %s", len(missing), strings.Join(names, ", "))
}
