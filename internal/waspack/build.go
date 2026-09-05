package waspack

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
)

var waspackRename = os.Rename
var waspackRemoveAll = os.RemoveAll

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
	ReplaceToken   string
	// ReplaceAuthorized is set only after the HTTP layer consumes a one-time
	// token bound to OutputDir. A boolean confirmation alone is insufficient.
	ReplaceAuthorized bool
	PackType          string // "app" (默认) 或 "batch"
	BatchBaseDir      string // 批量部署根路径，默认 /batch/credit
	ChmodMode         string // 批量 chmod 权限，默认 777（兼容现网，可显式配置）
	IncludeZip        bool   // 历史/重建请求是否同时生成 ZIP
	MetadataDir       string // 外部 provenance/ownership 元数据目录（由 HTTP Server 注入）
	StageToken        string // Extract 返回的 WAR stage token
}

const (
	OutputPolicyFail       = "fail"
	OutputPolicyCleanOwned = "clean_kairo_artifacts"
	OutputPolicyReplace    = "replace"
	legacyOutputMarkerName = ".kairo-waspack.json"
)

// Result 是生成结果。输出目录里包含 list.txt、chmod.txt（批量时）、Bak{包名}.sh、{包名}.sh、{包名}.tar。
type Result struct {
	OK            bool             `json:"ok"`
	OutputDir     string           `json:"output_dir"`
	TarFile       string           `json:"tar_file"`
	ListFile      string           `json:"list_file"`
	ChmodFile     string           `json:"chmod_file,omitempty"`
	ChmodContent  string           `json:"chmod_content,omitempty"`
	BackupScript  string           `json:"backup_script"`
	ExecuteScript string           `json:"execute_script"`
	PackageName   string           `json:"package_name"`
	Files         int              `json:"files"`
	Bytes         int64            `json:"bytes"`
	CreatedDir    bool             `json:"created_dir"`
	Warnings      []string         `json:"warnings,omitempty"`
	PairedAdded   int              `json:"paired_added"`
	WarDir        string           `json:"war_dir,omitempty"`
	PackType      string           `json:"pack_type,omitempty"`
	ChmodMode     string           `json:"chmod_mode,omitempty"`
	Artifacts     []ArtifactDigest `json:"artifacts,omitempty"`
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

// prepareOutputDirForStaging validates policy without mutating existing
// contents. Generation happens in a sibling temporary directory, then the
// completed artifacts are published together. This keeps a previous good
// package intact when preview, tar, or script generation fails.
func prepareOutputDirForStaging(path, policy string, confirmReplace bool, packageName string) (string, bool, error) {
	if strings.TrimSpace(path) == "" {
		return "", false, fmt.Errorf("输出目录不能为空")
	}
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || !isSafeLocalPath(abs) {
		return "", false, fmt.Errorf("输出目录非法")
	}
	st, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", false, fmt.Errorf("创建输出目录失败: %w", err)
		}
		return abs, true, nil
	}
	if err != nil {
		return "", false, err
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return "", false, fmt.Errorf("输出路径不是安全目录")
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		return "", false, err
	}
	if len(ents) == 0 {
		return abs, false, nil
	}
	switch normalizeOutputPolicy(policy) {
	case OutputPolicyReplace:
		if !confirmReplace {
			return "", false, fmt.Errorf("覆盖输出目录需要明确确认")
		}
	case OutputPolicyCleanOwned:
		// Existing files are removed only after the staged package has fully
		// validated. This is intentionally non-destructive at this point.
		_ = packageName
	default:
		return "", false, fmt.Errorf("输出目录不是空文件夹，拒绝覆盖: %s", abs)
	}
	return abs, false, nil
}

func publishStagedArtifacts(stage, outAbs, policy string, confirmReplace bool, names []string) error {
	return publishStagedArtifactsWithMetadata(stage, outAbs, policy, confirmReplace, names, "")
}

func publishStagedArtifactsWithMetadata(stage, outAbs, policy string, confirmReplace bool, names []string, metadataDir string) error {
	mode := normalizeOutputPolicy(policy)
	for _, name := range names {
		if !validOwnedChildName(name) {
			return fmt.Errorf("非法产物名 %q", name)
		}
		if _, err := os.Lstat(filepath.Join(stage, name)); err != nil {
			return fmt.Errorf("临时产物缺失 %s: %w", name, err)
		}
	}
	backup, err := os.MkdirTemp(filepath.Dir(outAbs), ".kairo-waspack-old-")
	if err != nil {
		return fmt.Errorf("创建旧产物备份目录失败: %w", err)
	}
	// Remove empty backups on validation/fully restored failures, but preserve
	// any files left by an incomplete rollback for manual recovery.
	defer os.Remove(backup)
	if mode == OutputPolicyReplace {
		if !confirmReplace {
			return fmt.Errorf("覆盖输出目录需要明确确认")
		}
		if err := moveChildrenToBackup(outAbs, backup); err != nil {
			return err
		}
	} else if mode == OutputPolicyCleanOwned {
		recordedNames, hasRecord, ownershipErr := ownedArtifactsAt(outAbs, metadataDir)
		if ownershipErr != nil {
			return fmt.Errorf("读取输出目录归属失败: %w", ownershipErr)
		}
		ownedNames := recordedNames
		if !hasRecord {
			// Legacy marker is only allowed to authorize the historical,
			// currently requested artifact names. Without it, a same-named
			// user file must fail closed.
			ownedNames = knownArtifactNames(namesToPackageNames(names)...)
			existingOwnedNames := existingChildren(outAbs, ownedNames)
			if _, err := os.Lstat(filepath.Join(outAbs, legacyOutputMarkerName)); os.IsNotExist(err) && len(existingOwnedNames) > 0 {
				return ownershipError(outAbs)
			}
		}
		owned := make(map[string]bool, len(ownedNames))
		for _, name := range ownedNames {
			owned[name] = true
		}
		for _, name := range names {
			if _, err := os.Lstat(filepath.Join(outAbs, name)); err == nil {
				if !owned[name] {
					return ownershipError(outAbs)
				}
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		if err := moveNamedToBackup(outAbs, backup, ownedNames); err != nil {
			return err
		}
	} else {
		ents, err := os.ReadDir(outAbs)
		if err != nil {
			return err
		}
		if len(ents) != 0 {
			return fmt.Errorf("发布前输出目录已被其他进程写入，拒绝覆盖")
		}
	}
	published := make([]string, 0, len(names))
	for _, name := range names {
		src := filepath.Join(stage, name)
		if _, err := os.Lstat(src); err != nil {
			if rollbackErr := rollbackPublished(outAbs, backup, published); rollbackErr != nil {
				return fmt.Errorf("临时产物缺失 %s: %w；回滚失败，旧产物备份保留于 %s: %v", name, err, backup, rollbackErr)
			}
			_ = waspackRemoveAll(backup)
			return fmt.Errorf("临时产物缺失 %s: %w", name, err)
		}
		if err := waspackRename(src, filepath.Join(outAbs, name)); err != nil {
			if rollbackErr := rollbackPublished(outAbs, backup, published); rollbackErr != nil {
				return fmt.Errorf("发布产物 %s 失败: %w；回滚失败，旧产物备份保留于 %s: %v", name, err, backup, rollbackErr)
			}
			_ = waspackRemoveAll(backup)
			return fmt.Errorf("发布产物 %s 失败: %w", name, err)
		}
		published = append(published, name)
	}
	if err := waspackRemoveAll(backup); err != nil {
		return fmt.Errorf("产物已发布，但旧产物备份无法清理，请手动检查 %s: %w", backup, err)
	}
	return nil
}

func namesToPackageNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if strings.HasSuffix(n, ".tar") {
			out = append(out, strings.TrimSuffix(n, ".tar"))
		}
	}
	return out
}

func knownArtifactNames(packageNames ...string) []string {
	set := map[string]bool{legacyOutputMarkerName: true, ExtractedWARDirName: true, ListFileName: true, ChmodFileName: true}
	for _, name := range packageNames {
		if name == "" {
			continue
		}
		set[TarFileName(name)] = true
		set[BackupScriptName(name)] = true
		set[ExecuteScriptName(name)] = true
		set[name+".zip"] = true
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	return out
}

func existingChildren(dir string, names []string) []string {
	existing := make([]string, 0, len(names))
	for _, name := range names {
		if !validOwnedChildName(name) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			existing = append(existing, name)
		}
	}
	return existing
}

func moveChildrenToBackup(outAbs, backup string) error {
	ents, err := os.ReadDir(outAbs)
	if err != nil {
		return err
	}
	for _, ent := range ents {
		if err := waspackRename(filepath.Join(outAbs, ent.Name()), filepath.Join(backup, ent.Name())); err != nil {
			if rollbackErr := rollbackPublished(outAbs, backup, nil); rollbackErr != nil {
				return fmt.Errorf("备份旧产物失败: %w；回滚失败，备份保留于 %s: %v", err, backup, rollbackErr)
			}
			return fmt.Errorf("备份旧产物失败: %w", err)
		}
	}
	return nil
}

func moveNamedToBackup(outAbs, backup string, names []string) error {
	for _, name := range names {
		src := filepath.Join(outAbs, name)
		if _, err := os.Lstat(src); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := waspackRename(src, filepath.Join(backup, name)); err != nil {
			if rollbackErr := rollbackPublished(outAbs, backup, nil); rollbackErr != nil {
				return fmt.Errorf("备份旧产物失败: %w；回滚失败，备份保留于 %s: %v", err, backup, rollbackErr)
			}
			return fmt.Errorf("备份旧产物失败: %w", err)
		}
	}
	return nil
}

func rollbackPublished(outAbs, backup string, published []string) error {
	var failures []string
	for _, name := range published {
		if err := waspackRemoveAll(filepath.Join(outAbs, name)); err != nil {
			failures = append(failures, "删除新产物 "+name+": "+err.Error())
		}
	}
	ents, err := os.ReadDir(backup)
	if err != nil {
		return err
	}
	for _, ent := range ents {
		if err := waspackRename(filepath.Join(backup, ent.Name()), filepath.Join(outAbs, ent.Name())); err != nil {
			failures = append(failures, "恢复 "+ent.Name()+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

type outputLockEntry struct {
	mu   sync.Mutex
	refs int
}

var outputLocks = struct {
	sync.Mutex
	entries map[string]*outputLockEntry
}{entries: map[string]*outputLockEntry{}}

func lockOutputDir(raw string) (func(), error) {
	if strings.TrimSpace(raw) == "" {
		return func() {}, fmt.Errorf("输出目录不能为空")
	}
	abs, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil {
		return func() {}, err
	}
	key, err := canonicalPath(abs)
	if err != nil {
		// For a new path, canonicalPath can only fail if no ancestor exists;
		// Abs is still a stable lock key and validation will report the real
		// filesystem error later.
		key = filepath.Clean(abs)
	}
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	outputLocks.Lock()
	entry := outputLocks.entries[key]
	if entry == nil {
		entry = &outputLockEntry{}
		outputLocks.entries[key] = entry
	}
	entry.refs++
	outputLocks.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		outputLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(outputLocks.entries, key)
		}
		outputLocks.Unlock()
	}, nil
}

// canonicalPath resolves the nearest existing parent before appending the
// non-existent suffix. EvalSymlinks alone cannot protect a new output path
// beneath a symlink/junction parent; resolving the ancestor closes that gap.
func canonicalPath(raw string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	cur := abs
	suffix := make([]string, 0, 4)
	for {
		st, statErr := os.Lstat(cur)
		if statErr == nil {
			if st.Mode()&os.ModeSymlink != 0 && cur == abs {
				return "", fmt.Errorf("路径本身是符号链接")
			}
			real, evalErr := filepath.EvalSymlinks(cur)
			if evalErr != nil {
				return "", evalErr
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return filepath.Clean(real), nil
		}
		if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("路径没有可解析的父目录")
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

// validateOutputAgainstProject prevents destructive output policies from
// touching the project itself, one of its ancestors, or a path which reaches
// the project through a symlink/junction parent. The project argument may be
// empty for the extracted-WAR packaging endpoint, where the staged metadata
// is not available to the caller.
func validateOutputAgainstProject(projectDir, outputDir string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(outputDir))
	if err != nil || !isSafeLocalPath(abs) {
		return "", fmt.Errorf("输出目录非法")
	}
	canonOut, err := canonicalPath(abs)
	if err != nil {
		return "", fmt.Errorf("输出目录无法解析: %w", err)
	}
	if strings.TrimSpace(projectDir) == "" {
		return canonOut, nil
	}
	projectAbs, err := filepath.Abs(strings.TrimSpace(projectDir))
	if err != nil {
		return "", fmt.Errorf("工程目录无效: %w", err)
	}
	projectInfo, err := os.Stat(projectAbs)
	if err != nil || !projectInfo.IsDir() {
		return "", fmt.Errorf("工程目录无效")
	}
	canonProject, err := canonicalPath(projectAbs)
	if err != nil {
		return "", fmt.Errorf("工程目录无法解析: %w", err)
	}
	if isSubpathOrEqual(canonOut, canonProject) || isSubpathOrEqual(canonProject, canonOut) {
		return "", fmt.Errorf("输出目录不能是工程目录本身、工程子目录或工程祖先目录")
	}
	return canonOut, nil
}

func prepareOutputDirWithPolicy(path, policy string, confirmReplace bool, packageNames ...string) (abs string, created bool, err error) {
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
		if err := cleanKnownOutputArtifacts(abs, packageNames...); err != nil {
			return "", false, err
		}
	case OutputPolicyReplace:
		if !confirmReplace {
			return "", false, fmt.Errorf("覆盖输出目录需要明确确认")
		}
		// 第三项为强制清空：只要用户已二次确认且路径通过安全校验，就清空目录内全部内容，
		// 不再要求必须是 Kairo 产物目录（含 marker）。误删风险由前端二次确认承担。
		if err := clearOutputChildren(abs); err != nil {
			return "", false, err
		}
	default:
		return "", false, fmt.Errorf("输出目录不是空文件夹，拒绝覆盖: %s", abs)
	}
	return abs, false, nil
}

func normalizeOutputPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case OutputPolicyCleanOwned:
		return OutputPolicyCleanOwned
	case OutputPolicyReplace:
		return OutputPolicyReplace
	default:
		return OutputPolicyFail
	}
}

func clearOutputChildren(abs string) error {
	ents, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, ent := range ents {
		if err := os.RemoveAll(filepath.Join(abs, ent.Name())); err != nil {
			return fmt.Errorf("清理输出目录失败: %w", err)
		}
	}
	return nil
}

func cleanKnownOutputArtifacts(abs string, packageNames ...string) error {
	owned := map[string]bool{
		legacyOutputMarkerName: true,
		ExtractedWARDirName:    true,
		ListFileName:           true,
		ChmodFileName:          true,
	}
	for _, rawName := range packageNames {
		if strings.TrimSpace(rawName) == "" {
			continue
		}
		name, err := SanitizePackageName(rawName)
		if err != nil {
			return err
		}
		owned[TarFileName(name)] = true
		owned[BackupScriptName(name)] = true
		owned[ExecuteScriptName(name)] = true
		owned[name+".zip"] = true
	}
	ownedNames := make([]string, 0, len(owned))
	for name := range owned {
		if _, err := os.Lstat(filepath.Join(abs, name)); err == nil {
			ownedNames = append(ownedNames, name)
		}
	}
	if _, err := os.Lstat(filepath.Join(abs, legacyOutputMarkerName)); os.IsNotExist(err) && len(ownedNames) > 0 && !ownsArtifacts(abs, ownedNames) {
		return ownershipError(abs)
	}
	for name := range owned {
		if err := os.RemoveAll(filepath.Join(abs, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("清理旧打包产物失败: %w", err)
		}
	}
	return nil
}

func isSafeLocalPath(p string) bool {
	return isSafeLocalPathForGOOS(p, runtime.GOOS)
}

func isSafeLocalPathForGOOS(p, goos string) bool {
	if p == "" {
		return false
	}
	// A foreign Windows drive path is only a relative filename on Unix-like
	// hosts. Reject it explicitly instead of letting platform-specific
	// filepath semantics bypass the sensitive-path checks below.
	if goos != "windows" && hasWindowsDrivePrefix(p) {
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
		tempRoot := filepath.Clean(os.TempDir())
		if !isSamePath(clean, tempRoot) && isSubpathOrEqual(clean, tempRoot) {
			return true
		}
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
	slashClean := portableSlashPath(clean)
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
		"C:/Windows",
		"C:/Program Files",
		"C:/Program Files (x86)",
		"C:/ProgramData",
	}
	for _, env := range []string{"SystemRoot", "windir", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
		if val := os.Getenv(env); val != "" {
			winPrefixes = append(winPrefixes, val)
		}
	}
	for _, pref := range winPrefixes {
		normalizedPrefix := portableSlashPath(pref)
		if slashClean == normalizedPrefix || strings.HasPrefix(slashClean, normalizedPrefix+"/") {
			return true
		}
	}
	usersDir := "C:/Users"
	if drive := os.Getenv("SystemDrive"); drive != "" {
		usersDir = drive + "/Users"
	}
	if slashClean == portableSlashPath(usersDir) {
		return true
	}
	return false
}

func hasWindowsDrivePrefix(p string) bool {
	p = strings.TrimSpace(p)
	return len(p) >= 2 && p[1] == ':' && (p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}

func portableSlashPath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return strings.ToLower(strings.TrimSuffix(p, "/"))
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

// IsBatchPack 统一判断是否为批量打包：显式 pack_type 为准，空时按包名前缀 DDD 兼容。
func IsBatchPack(packType, packageName string) bool {
	t := strings.ToLower(strings.TrimSpace(packType))
	if t == "batch" {
		return true
	}
	if t == "app" {
		return false
	}
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(packageName)), "DDD")
}

func listText(files []ResolvedFile) string {
	return listTextBatch(files, false)
}

// listTextBatch 批量与应用一致：统一带 ./ 前缀（如 ./amargci/...），
// 保证预检清单、list.txt、tar、脚本四者完全一致。
func listTextBatch(files []ResolvedFile, isBatch bool) string {
	var b strings.Builder
	for i, rf := range files {
		if i > 0 {
			b.WriteByte('\n')
		}
		_ = isBatch
		clean := strings.TrimPrefix(strings.ReplaceAll(rf.Rel, "\\", "/"), "./")
		clean = strings.TrimLeft(clean, "/")
		b.WriteString("./" + clean)
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
	if _, err := validateProjectDir(req.ProjectDir); err != nil {
		return nil, err
	}
	listed, err := ParseManifest(req.Manifest, req.ProjectDir)
	if err != nil {
		return nil, err
	}
	if IsBatchPack(req.PackType, req.PackageName) {
		return ResolveBatch(req.ProjectDir, listed, req.AutoPair)
	}
	return Resolve(req.ProjectDir, listed, req.AutoPair)
}

func validateProjectDir(projectDir string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(projectDir))
	if err != nil || !isSafeLocalPath(abs) {
		return "", fmt.Errorf("工程目录非法")
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("工程目录无效或为符号链接")
	}
	return abs, nil
}

// Build 创建空输出目录，写入 list.txt、tar、备份脚本、执行脚本（批量时另写 chmod.txt）。
func Build(req Request) (*Result, error) {
	if err := validateReplaceAuthorization(req); err != nil {
		return nil, err
	}
	unlock, err := lockOutputDir(req.OutputDir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return buildLocked(req)
}

func validateReplaceAuthorization(req Request) error {
	if normalizeOutputPolicy(req.OutputPolicy) == OutputPolicyReplace && (!req.ConfirmReplace || strings.TrimSpace(req.ReplaceToken) == "" || !req.ReplaceAuthorized) {
		return fmt.Errorf("覆盖输出目录需要有效的一次性确认凭据")
	}
	return nil
}

func buildLocked(req Request) (*Result, error) {
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
	if _, err := validateOutputAgainstProject(req.ProjectDir, req.OutputDir); err != nil {
		return nil, err
	}

	outAbs, created, err := prepareOutputDirForStaging(req.OutputDir, req.OutputPolicy, req.ConfirmReplace, pkgName)
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(outAbs), ".kairo-waspack-stage-")
	if err != nil {
		if created {
			_ = os.Remove(outAbs)
		}
		return nil, fmt.Errorf("创建临时打包目录失败: %w", err)
	}
	defer os.RemoveAll(stage)

	listPath := filepath.Join(stage, ListFileName)
	tarPath := filepath.Join(stage, TarFileName(pkgName))
	backupPath := filepath.Join(stage, BackupScriptName(pkgName))
	execPath := filepath.Join(stage, ExecuteScriptName(pkgName))

	isBatch := IsBatchPack(req.PackType, pkgName)
	var chmodPath, chmodContent string
	if isBatch {
		chmodContent, err = renderChmodScriptSafe(req.BatchBaseDir, pv.Files, req.ChmodMode)
		if err != nil {
			cleanup := func() {
				if created {
					_ = os.Remove(outAbs)
				}
			}
			cleanup()
			return nil, err
		}
		if chmodContent != "" {
			chmodPath = filepath.Join(stage, ChmodFileName)
		}
	}

	cleanup := func() { rollback(stage, false, listPath, tarPath, backupPath, execPath, chmodPath) }

	listContent := listTextBatch(pv.Files, isBatch)
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
	bytes, err := writeTar(tarPath, pv.Files, isBatch)
	if err != nil {
		cleanup()
		return nil, err
	}

	var backupContent, execContent string
	if isBatch {
		backupContent, err = renderBatchBackupScriptAtRoot(pkgName, pv.Files, req.BatchBaseDir)
		if err != nil {
			cleanup()
			return nil, err
		}
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
	artifactNames := []string{ListFileName, TarFileName(pkgName), BackupScriptName(pkgName), ExecuteScriptName(pkgName)}
	if chmodPath != "" {
		artifactNames = append(artifactNames, ChmodFileName)
	}
	if err := publishStagedArtifactsWithMetadata(stage, outAbs, req.OutputPolicy, req.ConfirmReplace, artifactNames, req.MetadataDir); err != nil {
		if created {
			_ = os.Remove(outAbs)
		}
		return nil, err
	}
	listPath = filepath.Join(outAbs, ListFileName)
	tarPath = filepath.Join(outAbs, TarFileName(pkgName))
	backupPath = filepath.Join(outAbs, BackupScriptName(pkgName))
	execPath = filepath.Join(outAbs, ExecuteScriptName(pkgName))
	if chmodPath != "" {
		chmodPath = filepath.Join(outAbs, ChmodFileName)
	}
	warnings := append([]string(nil), pv.Warnings...)
	if ownershipErr := reconcileOwnedArtifactsAt(outAbs, req.MetadataDir, artifactNames); ownershipErr != nil {
		warnings = append(warnings, "无法写入清理归属元数据："+ownershipErr.Error())
	}

	result := &Result{
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
		Warnings:      warnings,
		PairedAdded:   pairedCount(pv.Files),
		PackType:      req.PackType,
		ChmodMode:     req.ChmodMode,
	}
	artifacts, digestWarnings := ResultArtifacts(result)
	result.Artifacts = artifacts
	result.Warnings = append(result.Warnings, digestWarnings...)
	return result, nil
}
