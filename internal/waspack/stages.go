package waspack

import (
	"errors"
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
	StageToken  string   `json:"stage_token,omitempty"`
}

// Extract 只抽取，不创建 tar / list / shell 脚本。目标目录必须为空，避免把旧文件
// 混入新投产包。$1/$2 等内部类已经在 Resolve 的 autoPair 阶段一并收集。
func Extract(req Request) (*ExtractResult, error) {
	if err := validateReplaceAuthorization(req); err != nil {
		return nil, err
	}
	unlock, err := lockOutputDir(req.OutputDir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return extractLocked(req)
}

func extractLocked(req Request) (*ExtractResult, error) {
	pv, err := PreviewRequest(req)
	if err != nil {
		return nil, err
	}
	if len(pv.Missing) > 0 {
		return nil, missingFilesError(pv.Missing)
	}
	if _, err := validateOutputAgainstProject(req.ProjectDir, req.OutputDir); err != nil {
		return nil, err
	}
	outAbs, created, err := prepareOutputDirForStaging(req.OutputDir, req.OutputPolicy, req.ConfirmReplace, req.PackageName)
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(outAbs), ".kairo-waspack-stage-")
	if err != nil {
		if created {
			_ = os.Remove(outAbs)
		}
		return nil, fmt.Errorf("创建临时抽取目录失败: %w", err)
	}
	defer os.RemoveAll(stage)
	warDir := filepath.Join(stage, ExtractedWARDirName)
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
		// 兼容应用（./开头）与批量（不带 ./）两种 Rel 形式
		clean := strings.ReplaceAll(rf.Rel, "\\", "/")
		clean = strings.TrimPrefix(clean, "./")
		clean = strings.TrimLeft(clean, "/")
		rel := filepath.FromSlash(clean)
		dst := filepath.Join(warDir, rel)
		if err := copyExtractedFile(rf.Abs, dst, rf.info); err != nil {
			return nil, fmt.Errorf("抽取 %s 失败: %w", rf.Rel, err)
		}
		total += rf.Bytes
	}
	finalWar := filepath.Join(outAbs, ExtractedWARDirName)
	var stageWarn string
	if err := publishSingleStagedArtifactWithMetadata(stage, outAbs, req.OutputPolicy, req.ConfirmReplace, ExtractedWARDirName, req.MetadataDir); err != nil {
		var warn *BackupCleanupWarning
		if errors.As(err, &warn) {
			stageWarn = warn.Error()
		} else {
			return nil, err
		}
	}
	warDir = finalWar
	ok = true
	warnings := append([]string(nil), pv.Warnings...)
	if stageWarn != "" {
		warnings = append(warnings, stageWarn)
	}
	if ownershipErr := reconcileOwnedArtifactsAt(outAbs, req.MetadataDir, []string{ExtractedWARDirName}); ownershipErr != nil {
		warnings = append(warnings, "无法写入清理归属元数据："+ownershipErr.Error())
	}
	stageToken := ""
	if req.MetadataDir != "" {
		stageToken, err = WriteStageProvenance(req, outAbs)
		if err != nil {
			warnings = append(warnings, "无法写入抽取阶段凭据："+err.Error())
		}
	}
	return &ExtractResult{OK: true, OutputDir: outAbs, WarDir: warDir, Files: len(pv.Files), Bytes: total, Warnings: warnings, PairedAdded: pairedCount(pv.Files), StageToken: stageToken}, nil
}

func copyExtractedFile(src, dst string, expected ...os.FileInfo) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := openRegularFile(src, expected...)
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
	if err := validateReplaceAuthorization(req); err != nil {
		return nil, err
	}
	unlock, err := lockOutputDir(req.OutputDir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return packageExtractedLocked(req)
}

func packageExtractedLocked(req Request) (*Result, error) {
	pkgName, err := SanitizePackageName(req.PackageName)
	if err != nil {
		return nil, err
	}
	outAbs, err := filepath.Abs(strings.TrimSpace(req.OutputDir))
	if err != nil || !isSafeLocalPath(outAbs) {
		return nil, fmt.Errorf("打包目录无效")
	}
	if _, err := validateOutputAgainstProject(req.ProjectDir, outAbs); err != nil {
		return nil, err
	}
	if req.MetadataDir != "" {
		if err := ValidateStageProvenance(req, outAbs); err != nil {
			return nil, err
		}
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
	stage, err := os.MkdirTemp(filepath.Dir(outAbs), ".kairo-waspack-stage-")
	if err != nil {
		return nil, fmt.Errorf("创建临时打包目录失败: %w", err)
	}
	defer os.RemoveAll(stage)
	listPath := filepath.Join(stage, ListFileName)
	tarPath := filepath.Join(stage, TarFileName(pkgName))
	backupPath := filepath.Join(stage, BackupScriptName(pkgName))
	execPath := filepath.Join(stage, ExecuteScriptName(pkgName))
	// 批量时 chmod.txt 也是产物，必须纳入覆盖清理范围，否则二次打包会报“未知文件”。
	artifactNames := []string{filepath.Base(listPath), filepath.Base(tarPath), filepath.Base(backupPath), filepath.Base(execPath)}
	if err := prepareExistingArtifacts(outAbs, req.OutputPolicy, req.ConfirmReplace, artifactNames); err != nil {
		return nil, err
	}
	isBatch := IsBatchPack(req.PackType, pkgName)
	// war 扫描默认带 ./ 前缀，批量与应用一致：统一保留 ./（如 ./amargci/...），
	// 保证与预检清单、list.txt、tar、脚本四者完全一致。
	for i := range files {
		clean := strings.TrimPrefix(strings.ReplaceAll(files[i].Rel, "\\", "/"), "./")
		clean = strings.TrimLeft(clean, "/")
		files[i].Rel = "./" + clean
	}
	var chmodPath, chmodContent string
	if isBatch {
		chmodContent, err = renderChmodScriptSafe(req.BatchBaseDir, files, req.ChmodMode)
		if err != nil {
			return nil, err
		}
		if chmodContent != "" {
			chmodPath = filepath.Join(stage, ChmodFileName)
			artifactNames = append(artifactNames, ChmodFileName)
		}
	}
	cleanup := func() { rollback(stage, false, listPath, tarPath, backupPath, execPath, chmodPath) }
	if err := writeUnixFile(listPath, listTextBatch(files, isBatch), 0o644); err != nil {
		cleanup()
		return nil, fmt.Errorf("写清单失败: %w", err)
	}
	if chmodPath != "" && chmodContent != "" {
		if err := writeUnixFile(chmodPath, chmodContent, 0o644); err != nil {
			cleanup()
			return nil, fmt.Errorf("写赋权脚本失败: %w", err)
		}
	}
	bytes, err := writeTar(tarPath, files, isBatch)
	if err != nil {
		cleanup()
		return nil, err
	}
	var backupContent, execContent string
	if isBatch {
		backupContent, err = renderBatchBackupScriptAtRoot(pkgName, files, req.BatchBaseDir)
		if err != nil {
			cleanup()
			return nil, err
		}
		execContent = renderBatchExecuteScript(pkgName, files)
	} else {
		backupContent = renderBackupScript(pkgName, files)
		execContent = renderExecuteScript(pkgName, files)
	}
	if err := writeUnixFile(backupPath, backupContent, 0o755); err != nil {
		cleanup()
		return nil, fmt.Errorf("写备份脚本失败: %w", err)
	}
	if err := writeUnixFile(execPath, execContent, 0o755); err != nil {
		cleanup()
		return nil, fmt.Errorf("写执行脚本失败: %w", err)
	}
	var publishWarning string
	if err := publishNamedStagedArtifactsWithMetadata(stage, outAbs, req.OutputPolicy, req.ConfirmReplace, artifactNames, req.MetadataDir); err != nil {
		var warn *BackupCleanupWarning
		if errors.As(err, &warn) {
			publishWarning = warn.Error()
		} else {
			return nil, err
		}
	}
	listPath = filepath.Join(outAbs, ListFileName)
	tarPath = filepath.Join(outAbs, TarFileName(pkgName))
	backupPath = filepath.Join(outAbs, BackupScriptName(pkgName))
	execPath = filepath.Join(outAbs, ExecuteScriptName(pkgName))
	if chmodPath != "" {
		chmodPath = filepath.Join(outAbs, ChmodFileName)
	}
	_ = os.Remove(filepath.Join(outAbs, legacyOutputMarkerName))
	warnings := []string(nil)
	if publishWarning != "" {
		warnings = append(warnings, publishWarning)
	}
	if ownershipErr := reconcileOwnedArtifactsAt(outAbs, req.MetadataDir, artifactNames); ownershipErr != nil {
		warnings = append(warnings, "无法写入清理归属元数据："+ownershipErr.Error())
	}
	result := &Result{OK: true, OutputDir: outAbs, WarDir: warDir, TarFile: tarPath, ListFile: listPath, ChmodFile: chmodPath, ChmodContent: chmodContent, BackupScript: backupPath, ExecuteScript: execPath, PackageName: pkgName, Files: len(files), Bytes: bytes, Warnings: warnings, PackType: req.PackType, ChmodMode: req.ChmodMode}
	artifacts, digestWarnings := ResultArtifacts(result)
	result.Artifacts = artifacts
	result.Warnings = append(result.Warnings, digestWarnings...)
	return result, nil
}

func prepareExistingArtifacts(outAbs, policy string, confirmReplace bool, names []string) error {
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(outAbs, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	policy = normalizeOutputPolicy(policy)
	if policy == OutputPolicyReplace {
		if !confirmReplace {
			return fmt.Errorf("覆盖已有打包产物需要明确确认")
		}
		return nil
	}
	allowed := map[string]bool{ExtractedWARDirName: true, legacyOutputMarkerName: true}
	for _, name := range names {
		allowed[name] = true
	}
	// zip 产物也视为已知，避免打包后打 zip 再二次打包时误报未知文件
	allowed[".zip"] = true
	entries, err := os.ReadDir(outAbs)
	if err != nil {
		return err
	}
	if policy == OutputPolicyCleanOwned {
		return nil
	}
	for _, entry := range entries {
		if allowed[entry.Name()] || strings.HasSuffix(strings.ToLower(entry.Name()), ".zip") {
			continue
		}
		return fmt.Errorf("输出目录包含未知文件 %q，拒绝混入投产包", entry.Name())
	}
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(outAbs, name)); err == nil {
			return fmt.Errorf("目标文件已存在，拒绝覆盖: %s", filepath.Join(outAbs, name))
		}
	}
	return nil
}

func publishNamedStagedArtifacts(stage, outAbs, policy string, confirmReplace bool, names []string) error {
	return publishNamedStagedArtifactsWithMetadata(stage, outAbs, policy, confirmReplace, names, "")
}

func publishNamedStagedArtifactsWithMetadata(stage, outAbs, policy string, confirmReplace bool, names []string, metadataDir string) error {
	policy = normalizeOutputPolicy(policy)
	if policy == OutputPolicyReplace && !confirmReplace {
		return fmt.Errorf("覆盖已有打包产物需要明确确认")
	}
	if policy == OutputPolicyCleanOwned {
		if _, markerErr := os.Lstat(filepath.Join(outAbs, legacyOutputMarkerName)); os.IsNotExist(markerErr) {
			for _, name := range names {
				if _, existsErr := os.Lstat(filepath.Join(outAbs, name)); existsErr == nil && !ownsArtifactsAt(outAbs, metadataDir, []string{name}) {
					return ownershipError(outAbs)
				}
			}
		}
	}
	backup, err := os.MkdirTemp(filepath.Dir(outAbs), ".kairo-waspack-old-")
	if err != nil {
		return fmt.Errorf("创建旧产物备份目录失败: %w", err)
	}
	keepBackup := true
	defer func() {
		if !keepBackup {
			_ = waspackRemoveAll(backup)
		}
	}()
	if err := moveNamedToBackup(outAbs, backup, names); err != nil {
		return err
	}
	published := make([]string, 0, len(names))
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(backup, name)); err == nil && policy == OutputPolicyFail {
			if rollbackErr := rollbackPublished(outAbs, backup, published); rollbackErr != nil {
				return fmt.Errorf("目标文件已存在，拒绝覆盖: %s；回滚失败，备份保留于 %s: %v", filepath.Join(outAbs, name), backup, rollbackErr)
			}
			_ = waspackRemoveAll(backup)
			keepBackup = false
			return fmt.Errorf("目标文件已存在，拒绝覆盖: %s", filepath.Join(outAbs, name))
		}
		if err := waspackRename(filepath.Join(stage, name), filepath.Join(outAbs, name)); err != nil {
			if rollbackErr := rollbackPublished(outAbs, backup, published); rollbackErr != nil {
				return fmt.Errorf("发布产物 %s 失败: %w；回滚失败，备份保留于 %s: %v", name, err, backup, rollbackErr)
			}
			_ = waspackRemoveAll(backup)
			keepBackup = false
			return fmt.Errorf("发布产物 %s 失败: %w", name, err)
		}
		published = append(published, name)
	}
	if err := waspackRemoveAll(backup); err != nil {
		keepBackup = true
		return &BackupCleanupWarning{BackupDir: backup, Cause: err}
	}
	keepBackup = false
	return nil
}

// publishSingleStagedArtifact is used for Extract's war directory. Replace
// semantics back up the entire output directory, while clean-owned only moves
// the recorded war directory. No old path is removed before staging succeeds.
func publishSingleStagedArtifact(stage, outAbs, policy string, confirmReplace bool, name string) error {
	return publishSingleStagedArtifactWithMetadata(stage, outAbs, policy, confirmReplace, name, "")
}

func publishSingleStagedArtifactWithMetadata(stage, outAbs, policy string, confirmReplace bool, name, metadataDir string) error {
	return publishStagedArtifactsWithMetadata(stage, outAbs, policy, confirmReplace, []string{name}, metadataDir)
}

func filesFromWAR(warDir string) ([]ResolvedFile, error) {
	var files []ResolvedFile
	var total int64
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
		if info.Size() < 0 || total > MaxArchiveBytes-info.Size() {
			return fmt.Errorf("war 内容超过 %d 字节上限", MaxArchiveBytes)
		}
		if len(files) >= MaxFiles {
			return fmt.Errorf("war 文件数超过 %d 上限", MaxFiles)
		}
		total += info.Size()
		rel, err := filepath.Rel(warDir, p)
		if err != nil {
			return err
		}
		rel = "./" + filepath.ToSlash(rel)
		files = append(files, ResolvedFile{Rel: rel, Abs: p, Kind: kindOf(rel), Source: "war", Bytes: info.Size(), Exists: true, info: info})
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
