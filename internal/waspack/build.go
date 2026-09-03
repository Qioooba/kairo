package waspack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var pkgNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$`)

// Request 是一次生成投产包的输入。用户只配工程根、输出目录、包名和清单。
type Request struct {
	ProjectDir     string
	OutputDir      string
	PackageName    string
	Manifest       string
	AutoPair       bool
	OutputPolicy   string
	ConfirmReplace bool
	PackType       string // "app" (默认) 或 "batch"
	BatchBaseDir   string // 批量部署根路径，默认 /batch/credit
}

const (
	OutputPolicyFail       = "fail"
	OutputPolicyCleanOwned = "clean_kairo_artifacts"
	OutputPolicyReplace    = "replace"
	outputMarkerName       = ".kairo-waspack.json"
)

type outputMarker struct {
	Owner       string   `json:"owner"`
	Version     int      `json:"version"`
	PackageName string   `json:"package_name,omitempty"`
	Owned       []string `json:"owned"`
	GeneratedAt string   `json:"generated_at"`
}

// Result 是生成结果。输出目录里包含 list.txt、chmod.txt（批量时）、Bak{包名}.sh、{包名}.sh、{包名}.tar。
type Result struct {
	OK            bool     `json:"ok"`
	OutputDir     string   `json:"output_dir"`
	TarFile       string   `json:"tar_file"`
	ListFile      string   `json:"list_file"`
	ChmodFile     string   `json:"chmod_file,omitempty"`
	ChmodContent  string   `json:"chmod_content,omitempty"`
	BackupScript  string   `json:"backup_script"`
	ExecuteScript string   `json:"execute_script"`
	PackageName   string   `json:"package_name"`
	Files         int      `json:"files"`
	Bytes         int64    `json:"bytes"`
	CreatedDir    bool     `json:"created_dir"`
	Warnings      []string `json:"warnings,omitempty"`
	PairedAdded   int      `json:"paired_added"`
	WarDir        string   `json:"war_dir,omitempty"`
	PackType      string   `json:"pack_type,omitempty"`
}

// SanitizePackageName 只允许字母数字和 ._- ，空则用 credit_YYYYMMDD。
func SanitizePackageName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "credit_" + time.Now().Format("20060102"), nil
	}
	name = strings.TrimSuffix(name, ".tar")
	name = strings.TrimSuffix(name, ".sh")
	if !pkgNameRe.MatchString(name) {
		return "", fmt.Errorf("包名只能用字母数字和 ._- ，且不能以符号开头")
	}
	return name, nil
}

func prepareOutputDir(path string) (abs string, created bool, err error) {
	return prepareOutputDirWithPolicy(path, OutputPolicyFail, false)
}

func prepareOutputDirWithPolicy(path, policy string, confirmReplace bool) (abs string, created bool, err error) {
	if strings.TrimSpace(path) == "" {
		return "", false, fmt.Errorf("输出目录不能为空")
	}
	if strings.ContainsAny(path, "\x00\n\r") {
		return "", false, fmt.Errorf("输出目录含非法字符")
	}
	abs, err = filepath.Abs(path)
	if err != nil {
		return "", false, fmt.Errorf("输出目录无效: %w", err)
	}
	if !isSafeLocalPath(abs) {
		return "", false, fmt.Errorf("输出目录非法")
	}
	st, err := os.Lstat(abs)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", false, err
		}
		if mkErr := os.MkdirAll(abs, 0o755); mkErr != nil {
			return "", false, fmt.Errorf("创建输出目录失败: %w", mkErr)
		}
		return abs, true, nil
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("输出路径是符号链接，拒绝写入")
	}
	if !st.IsDir() {
		return "", false, fmt.Errorf("输出路径已存在且不是目录")
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		return "", false, err
	}
	if len(ents) == 0 {
		return abs, false, nil
	}
	switch normalizeOutputPolicy(policy) {
	case OutputPolicyCleanOwned:
		if err := cleanOwnedOutput(abs); err != nil { return "", false, err }
	case OutputPolicyReplace:
		if !confirmReplace { return "", false, fmt.Errorf("覆盖输出目录需要明确确认") }
		if _, err := readOutputMarker(abs); err != nil {
			return "", false, fmt.Errorf("输出目录非空且未包含 Kairo 产物标记，拒绝清空非 Kairo 目录: %w", err)
		}
		if err := clearOutputChildren(abs); err != nil { return "", false, err }
	default:
		return "", false, fmt.Errorf("输出目录不是空文件夹，拒绝覆盖: %s", abs)
	}
	return abs, false, nil
}

func normalizeOutputPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case OutputPolicyCleanOwned: return OutputPolicyCleanOwned
	case OutputPolicyReplace: return OutputPolicyReplace
	default: return OutputPolicyFail
	}
}

func clearOutputChildren(abs string) error {
	ents, err := os.ReadDir(abs); if err != nil { return err }
	for _, ent := range ents {
		if err := os.RemoveAll(filepath.Join(abs, ent.Name())); err != nil { return fmt.Errorf("清理输出目录失败: %w", err) }
	}
	return nil
}

func readOutputMarker(abs string) (outputMarker, error) {
	raw, err := os.ReadFile(filepath.Join(abs, outputMarkerName))
	if err != nil { return outputMarker{}, fmt.Errorf("输出目录不是 Kairo 管理的产物目录，拒绝自动清理") }
	var marker outputMarker
	if err := json.Unmarshal(raw, &marker); err != nil || marker.Owner != "kairo-waspack" || marker.Version != 1 {
		return outputMarker{}, fmt.Errorf("Kairo 产物标记无效，拒绝自动清理")
	}
	return marker, nil
}

func cleanOwnedOutput(abs string) error {
	marker, err := readOutputMarker(abs); if err != nil { return err }
	owned := map[string]bool{outputMarkerName: true}
	for _, name := range marker.Owned {
		name = filepath.Clean(strings.TrimSpace(name))
		if name == "." || name == ".." || filepath.IsAbs(name) || strings.HasPrefix(name, ".."+string(filepath.Separator)) { return fmt.Errorf("产物标记包含非法路径") }
		owned[name] = true
	}
	ents, err := os.ReadDir(abs); if err != nil { return err }
	for _, ent := range ents {
		if !owned[ent.Name()] { return fmt.Errorf("输出目录包含未知文件 %q，拒绝自动清理", ent.Name()) }
	}
	for name := range owned {
		if name == outputMarkerName || name == "." { _ = os.Remove(filepath.Join(abs, name)); continue }
		_ = os.RemoveAll(filepath.Join(abs, name))
	}
	left, err := os.ReadDir(abs); if err != nil { return err }
	if len(left) > 0 { return fmt.Errorf("输出目录仍包含未清理的文件") }
	return nil
}

func writeOutputMarker(abs, packageName string, owned []string) error {
	marker := outputMarker{Owner: "kairo-waspack", Version: 1, PackageName: packageName, Owned: append([]string(nil), owned...), GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	tmpName := filepath.Join(abs, outputMarkerName+".tmp."+strconv.FormatInt(time.Now().UnixNano(), 10))
	targetName := filepath.Join(abs, outputMarkerName)
	if err := os.WriteFile(tmpName, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpName, targetName); err != nil {
		_ = os.Remove(targetName)
		if renameErr := os.Rename(tmpName, targetName); renameErr != nil {
			_ = os.Remove(tmpName)
			return renameErr
		}
	}
	return nil
}

func isSafeLocalPath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "~") {
		return false
	}
	for _, r := range p {
		if r < 32 || r == 127 {
			return false
		}
	}
	clean := filepath.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	volume := filepath.VolumeName(clean)
	rest := strings.Trim(clean[len(volume):], `/\\`)
	// Never allow a drive/UNC root or the process working directory as an
	// output target. The replace policy is deliberately destructive, so these
	// two guards prevent a mistyped path from turning into a broad cleanup.
	if rest == "" {
		return false
	}
	if cwd, err := os.Getwd(); err == nil && isSamePath(cwd, clean) {
		return false
	}
	if home, err := os.UserHomeDir(); err == nil && isSamePath(home, clean) {
		return false
	}
	vol := filepath.VolumeName(p)
	rest2 := strings.TrimPrefix(p, vol)
	for _, seg := range strings.FieldsFunc(rest2, func(r rune) bool {
		return r == filepath.Separator || r == '/' || r == '\\'
	}) {
		if seg == ".." {
			return false
		}
		if strings.IndexFunc(seg, unicode.IsControl) >= 0 {
			return false
		}
	}
	if isBlockedSystemPath(clean) {
		return false
	}
	return true
}

func isSamePath(a, b string) bool {
	ca := filepath.Clean(a)
	cb := filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}

func isSubpathOrEqual(target, base string) bool {
	if base == "" {
		return false
	}
	t := filepath.Clean(target)
	b := filepath.Clean(base)
	if runtime.GOOS == "windows" {
		t = strings.ToLower(t)
		b = strings.ToLower(b)
	}
	if t == b {
		return true
	}
	rel, err := filepath.Rel(b, t)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != "."
}

func isBlockedSystemPath(clean string) bool {
	slashClean := strings.ToLower(filepath.ToSlash(clean))
	unixRoots := []string{"/bin", "/sbin", "/etc", "/usr", "/var", "/boot", "/dev", "/proc", "/sys", "/root"}
	for _, r := range unixRoots {
		if slashClean == r || strings.HasPrefix(slashClean, r+"/") {
			return true
		}
	}
	if slashClean == "/home" || slashClean == "/" || slashClean == "" {
		return true
	}

	winPrefixes := []string{
		"C:\\Windows",
		"C:\\Program Files",
		"C:\\Program Files (x86)",
		"C:\\ProgramData",
	}
	for _, env := range []string{"SystemRoot", "windir", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
		if val := os.Getenv(env); val != "" {
			winPrefixes = append(winPrefixes, val)
		}
	}
	for _, pref := range winPrefixes {
		if isSubpathOrEqual(clean, pref) {
			return true
		}
	}
	usersDir := "C:\\Users"
	if drive := os.Getenv("SystemDrive"); drive != "" {
		usersDir = drive + "\\Users"
	}
	if isSamePath(clean, usersDir) {
		return true
	}
	return false
}

func writeUnixFile(path, content string, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	_, err = f.WriteString(content)
	return err
}

func listText(files []ResolvedFile) string {
	var b strings.Builder
	for i, rf := range files {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(rf.Rel)
	}
	b.WriteByte('\n')
	return b.String()
}

func pairedCount(files []ResolvedFile) int {
	n := 0
	for _, rf := range files {
		if rf.Source == "paired" {
			n++
		}
	}
	return n
}

func rollback(abs string, created bool, files ...string) {
	for _, f := range files {
		_ = os.Remove(f)
	}
	if created {
		_ = os.Remove(abs)
	}
}

// PreviewRequest 只解析清单并对照工程，不写盘。
func PreviewRequest(req Request) (*Preview, error) {
	if strings.TrimSpace(req.ProjectDir) == "" {
		return nil, fmt.Errorf("请选择本地工程目录")
	}
	listed, err := ParseManifest(req.Manifest, req.ProjectDir)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(strings.TrimSpace(req.PackType)) == "batch" {
		return ResolveBatch(req.ProjectDir, listed, req.AutoPair)
	}
	return Resolve(req.ProjectDir, listed, req.AutoPair)
}

// Build 创建空输出目录，写入 list.txt、tar、备份脚本、执行脚本（批量时另写 chmod.txt）。
func Build(req Request) (*Result, error) {
	pkgName, err := SanitizePackageName(req.PackageName)
	if err != nil {
		return nil, err
	}

	pv, err := PreviewRequest(req)
	if err != nil {
		return nil, err
	}
	if len(pv.Missing) > 0 {
		names := make([]string, 0, len(pv.Missing))
		for i, m := range pv.Missing {
			if i >= 8 {
				names = append(names, fmt.Sprintf("…另有 %d 个", len(pv.Missing)-8))
				break
			}
			names = append(names, m.Rel)
		}
		return nil, fmt.Errorf("工程里找不到 %d 个文件，请先编译或检查路径: %s", len(pv.Missing), strings.Join(names, ", "))
	}

	outAbs, created, err := prepareOutputDirWithPolicy(req.OutputDir, req.OutputPolicy, req.ConfirmReplace)
	if err != nil {
		return nil, err
	}

	listPath := filepath.Join(outAbs, ListFileName)
	tarPath := filepath.Join(outAbs, TarFileName(pkgName))
	backupPath := filepath.Join(outAbs, BackupScriptName(pkgName))
	execPath := filepath.Join(outAbs, ExecuteScriptName(pkgName))

	isBatch := strings.ToLower(strings.TrimSpace(req.PackType)) == "batch"
	var chmodPath, chmodContent string
	if isBatch {
		chmodContent = renderChmodScript(req.BatchBaseDir, pv.Files)
		if chmodContent != "" {
			chmodPath = filepath.Join(outAbs, ChmodFileName)
		}
	}

	cleanup := func() { rollback(outAbs, created, listPath, tarPath, backupPath, execPath, chmodPath) }

	listContent := listText(pv.Files)
	if err := writeUnixFile(listPath, listContent, 0o644); err != nil {
		cleanup()
		return nil, fmt.Errorf("写清单失败: %w", err)
	}
	if chmodPath != "" && chmodContent != "" {
		if err := writeUnixFile(chmodPath, chmodContent, 0o644); err != nil {
			cleanup()
			return nil, fmt.Errorf("写赋权脚本失败: %w", err)
		}
	}
	bytes, err := writeTar(tarPath, pv.Files)
	if err != nil {
		cleanup()
		return nil, err
	}

	var backupContent, execContent string
	if isBatch {
		backupContent = renderBatchBackupScript(pkgName, pv.Files)
		execContent = renderBatchExecuteScript(pkgName, pv.Files)
	} else {
		backupContent = renderBackupScript(pkgName, pv.Files)
		execContent = renderExecuteScript(pkgName, pv.Files)
	}

	if err := writeUnixFile(backupPath, backupContent, 0o755); err != nil {
		cleanup()
		return nil, fmt.Errorf("写备份脚本失败: %w", err)
	}
	if err := writeUnixFile(execPath, execContent, 0o755); err != nil {
		cleanup()
		return nil, fmt.Errorf("写执行脚本失败: %w", err)
	}

	return &Result{
		OK:            true,
		OutputDir:     outAbs,
		TarFile:       tarPath,
		ListFile:      listPath,
		ChmodFile:     chmodPath,
		ChmodContent:  chmodContent,
		BackupScript:  backupPath,
		ExecuteScript: execPath,
		PackageName:   pkgName,
		Files:         len(pv.Files),
		Bytes:         bytes,
		CreatedDir:    created,
		Warnings:      pv.Warnings,
		PairedAdded:   pairedCount(pv.Files),
		PackType:      req.PackType,
	}, nil
}
