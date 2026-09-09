// Package upgrade coordinates durable on-disk upgrades before application stores are opened.
//
// Every user-owned file keeps its own format version. This package adds the missing
// cross-file transaction boundary: snapshot all known files, stage migrations,
// atomically replace them, and commit the manifest last. A failed migration restores
// the snapshot so the previous executable can still open the data.
package upgrade

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ManifestSchemaVersion = 1
	manifestFileName      = "upgrade-state.json"
	lockFileName          = ".upgrade.lock"
	maxManagedFileSize    = 256 << 20
)

var errInvalidManifest = errors.New("upgrade: invalid manifest")

type Migration func(raw []byte) ([]byte, error)
type Validator func(raw []byte) error
type VersionDetector func(raw []byte) (int, error)

// Asset is one independently versioned user-owned file.
type Asset struct {
	Name           string
	Path           string
	CurrentVersion int
	Critical       bool
	Validate       Validator
	DetectVersion  VersionDetector
	Migrations     map[int]Migration // key is the source version; every migration advances exactly one version
}

type AssetState struct {
	Version int    `json:"version"`
	SHA256  string `json:"sha256,omitempty"`
}

type Manifest struct {
	SchemaVersion  int                   `json:"schema_version"`
	ProductVersion string                `json:"product_version"`
	Assets         map[string]AssetState `json:"assets"`
	UpdatedAt      string                `json:"updated_at"`
}

type Options struct {
	DataDir        string
	ProductVersion string
	Assets         []Asset
	Now            func() time.Time
	// writeAtomic is intentionally private: production callers always use the
	// durable atomic writer. Tests may inject a commit failure to prove that a
	// partially committed upgrade is rolled back before returning an error.
	writeAtomic func(path string, raw []byte, mode fs.FileMode) error
}

type Result struct {
	Adopted   bool
	BackupDir string
	Migrated  []string
	Warnings  []string
	Previous  Manifest
	Current   Manifest
}

type preparedAsset struct {
	asset Asset
	raw   []byte
	state AssetState
}

// Run validates and upgrades every registered file before application modules open them.
func Run(opts Options) (*Result, error) {
	if strings.TrimSpace(opts.DataDir) == "" {
		return nil, errors.New("upgrade: data directory is empty")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	commitWrite := opts.writeAtomic
	if commitWrite == nil {
		commitWrite = writeAtomic
	}
	if err := validateAssets(opts.Assets); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("upgrade: create data directory: %w", err)
	}
	release, err := acquireLock(filepath.Join(opts.DataDir, lockFileName), opts.Now())
	if err != nil {
		return nil, err
	}
	defer release()

	manifestPath := filepath.Join(opts.DataDir, manifestFileName)
	previous, exists, err := readManifest(manifestPath)
	manifestWarning := ""
	if errors.Is(err, errInvalidManifest) {
		manifestWarning = err.Error() + "; rebuilding it from validated user files"
		previous, exists, err = Manifest{}, false, nil
	}
	if err != nil {
		return nil, err
	}
	if exists && previous.SchemaVersion > ManifestSchemaVersion {
		return nil, fmt.Errorf("upgrade: state schema %d is newer than supported %d", previous.SchemaVersion, ManifestSchemaVersion)
	}
	if exists && compareProductVersions(opts.ProductVersion, previous.ProductVersion) < 0 {
		return nil, fmt.Errorf("upgrade: refusing product downgrade from %s data to %s executable; restore a matching upgrade snapshot first", previous.ProductVersion, opts.ProductVersion)
	}
	if previous.Assets == nil {
		previous.Assets = map[string]AssetState{}
	}

	result := &Result{Adopted: !exists, Previous: previous}
	if manifestWarning != "" {
		result.Warnings = append(result.Warnings, manifestWarning)
	}
	prepared := make([]preparedAsset, 0, len(opts.Assets))
	needsSnapshot := !exists

	for _, asset := range opts.Assets {
		raw, readErr := readManagedFile(asset.Path)
		if errors.Is(readErr, fs.ErrNotExist) {
			prepared = append(prepared, preparedAsset{asset: asset, state: AssetState{Version: asset.CurrentVersion}})
			continue
		}
		if readErr != nil {
			return nil, fmt.Errorf("upgrade: read %s: %w", asset.Name, readErr)
		}
		// 非关键文件的问题（校验失败、版本识别失败）只降级为警告：既不阻断启动，
		// 也绝不尝试迁移或覆盖它。关键文件则必须阻断整个升级事务。
		softFailure := func(stage string, cause error) bool {
			message := fmt.Sprintf("%s %s: %v", asset.Name, stage, cause)
			if asset.Critical {
				return false
			}
			result.Warnings = append(result.Warnings, message)
			prepared = append(prepared, preparedAsset{asset: asset, raw: raw, state: AssetState{Version: asset.CurrentVersion, SHA256: digest(raw)}})
			return true
		}
		if asset.Validate != nil {
			if validateErr := asset.Validate(raw); validateErr != nil {
				if softFailure("validation failed", validateErr) {
					continue
				}
				return nil, fmt.Errorf("upgrade: %s validation failed: %v", asset.Name, validateErr)
			}
		}

		from := asset.CurrentVersion
		if asset.DetectVersion != nil {
			detected, detectErr := asset.DetectVersion(raw)
			if detectErr != nil {
				if softFailure("version detection failed", detectErr) {
					continue
				}
				return nil, fmt.Errorf("upgrade: detect %s version: %w", asset.Name, detectErr)
			}
			from = detected
		} else if old, ok := previous.Assets[asset.Name]; ok {
			from = old.Version
		}
		if from < 0 {
			return nil, fmt.Errorf("upgrade: %s has invalid format version %d", asset.Name, from)
		}
		if from > asset.CurrentVersion {
			return nil, fmt.Errorf("upgrade: %s format %d is newer than supported %d", asset.Name, from, asset.CurrentVersion)
		}
		original := append([]byte(nil), raw...)
		for from < asset.CurrentVersion {
			migration := asset.Migrations[from]
			if migration == nil {
				return nil, fmt.Errorf("upgrade: %s has no migration from version %d", asset.Name, from)
			}
			raw, err = migration(raw)
			if err != nil {
				return nil, fmt.Errorf("upgrade: migrate %s from version %d: %w", asset.Name, from, err)
			}
			from++
		}
		if !bytes.Equal(original, raw) {
			needsSnapshot = true
			result.Migrated = append(result.Migrated, asset.Name)
		}
		prepared = append(prepared, preparedAsset{asset: asset, raw: raw, state: AssetState{Version: from, SHA256: digest(raw)}})
	}

	var snapshot *snapshot
	if needsSnapshot && hasExistingAssets(prepared) {
		snapshot, err = createSnapshot(opts.DataDir, prepared, opts.Now())
		if err != nil {
			return nil, err
		}
		result.BackupDir = snapshot.dir
	}

	rollback := func(cause error) (*Result, error) {
		if snapshot == nil {
			return nil, cause
		}
		if restoreErr := snapshot.restore(); restoreErr != nil {
			return nil, fmt.Errorf("%w; rollback also failed: %w", cause, restoreErr)
		}
		return nil, cause
	}

	for _, item := range prepared {
		if len(item.raw) == 0 || !contains(result.Migrated, item.asset.Name) {
			continue
		}
		if err := commitWrite(item.asset.Path, item.raw, 0o600); err != nil {
			return rollback(fmt.Errorf("upgrade: commit %s: %w", item.asset.Name, err))
		}
	}

	current := Manifest{
		SchemaVersion:  ManifestSchemaVersion,
		ProductVersion: strings.TrimSpace(opts.ProductVersion),
		Assets:         map[string]AssetState{},
		UpdatedAt:      opts.Now().UTC().Format(time.RFC3339Nano),
	}
	for _, item := range prepared {
		state := item.state
		if len(item.raw) > 0 && state.SHA256 == "" {
			state.SHA256 = digest(item.raw)
		}
		current.Assets[item.asset.Name] = state
	}
	// 幂等启动：清单与磁盘内容一致（同产品版本、同资产状态）时跳过重写，
	// 避免 updated_at 抖动，也让只读数据目录下的重复启动不再制造无意义写入。
	if exists && manifestWarning == "" && manifestsEquivalent(previous, current) {
		result.Current = current
		return result, nil
	}
	manifestRaw, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return rollback(fmt.Errorf("upgrade: marshal manifest: %w", err))
	}
	if err := commitWrite(manifestPath, manifestRaw, 0o600); err != nil {
		return rollback(fmt.Errorf("upgrade: commit manifest: %w", err))
	}
	result.Current = current
	return result, nil
}

// manifestsEquivalent 比较两份清单的语义内容（忽略 updated_at 时间戳）。
func manifestsEquivalent(previous, current Manifest) bool {
	if previous.SchemaVersion != current.SchemaVersion ||
		previous.ProductVersion != current.ProductVersion ||
		len(previous.Assets) != len(current.Assets) {
		return false
	}
	for name, state := range current.Assets {
		old, ok := previous.Assets[name]
		if !ok || old.Version != state.Version || old.SHA256 != state.SHA256 {
			return false
		}
	}
	return true
}

func validateAssets(assets []Asset) error {
	seenNames := map[string]bool{}
	seenPaths := map[string]bool{}
	for i := range assets {
		a := &assets[i]
		a.Name = strings.TrimSpace(a.Name)
		if a.Name == "" || strings.TrimSpace(a.Path) == "" || a.CurrentVersion < 1 {
			return fmt.Errorf("upgrade: invalid asset at index %d", i)
		}
		abs, err := filepath.Abs(a.Path)
		if err != nil {
			return fmt.Errorf("upgrade: resolve %s: %w", a.Name, err)
		}
		a.Path = abs
		if seenNames[a.Name] || seenPaths[abs] {
			return fmt.Errorf("upgrade: duplicate asset %q or path %q", a.Name, abs)
		}
		seenNames[a.Name], seenPaths[abs] = true, true
	}
	return nil
}

func readManagedFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("managed path must be a regular file, not a symlink")
	}
	if info.Size() > maxManagedFileSize {
		return nil, fmt.Errorf("file is too large: %d bytes", info.Size())
	}
	return os.ReadFile(path)
}

func readManifest(path string) (Manifest, bool, error) {
	raw, err := readManagedFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, fmt.Errorf("upgrade: read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, false, fmt.Errorf("%w: %v", errInvalidManifest, err)
	}
	return manifest, true, nil
}

func ValidateJSON(raw []byte) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if !json.Valid(raw) {
		return errors.New("invalid JSON")
	}
	return nil
}

func ValidateYAML(raw []byte) error {
	var node yaml.Node
	return yaml.Unmarshal(raw, &node)
}

func ValidateHexKey(raw []byte) error {
	value := strings.TrimSpace(string(raw))
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return errors.New("expected a 32-byte hexadecimal key")
	}
	return nil
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func hasExistingAssets(items []preparedAsset) bool {
	for _, item := range items {
		if len(item.raw) > 0 {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// SortWarnings makes logs and tests deterministic.
func (r *Result) SortWarnings() { sort.Strings(r.Warnings) }

func copyRegular(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
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
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
