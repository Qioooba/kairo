package upgrade

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kairo/internal/config"
)

func fixedNow() time.Time { return time.Date(2026, 9, 2, 10, 11, 12, 123456789, time.UTC) }

func TestDefaultAssetsInventoryIsCompleteAndUnique(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	assets := DefaultAssets(filepath.Join(t.TempDir(), "config.yaml"), t.TempDir(), t.TempDir())
	want := []string{
		"config", "preferences", "credentials", "database-sources", "pet", "browser-state",
		"ssh-compat-profiles", "reminders", "notes", "scheduled-tasks", "scheduled-task-runs",
		"http-cases", "wsdl-projects", "soap-templates", "soap-history", "soap-mocks",
		"soap-mock-records", "desktop-pet-preference", "credential-key", "pet-key",
		"download-metadata", "license-certificate",
	}
	got := map[string]bool{}
	for _, asset := range assets {
		if got[asset.Name] {
			t.Fatalf("duplicate durable asset %q", asset.Name)
		}
		got[asset.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("durable asset %q is missing from upgrade inventory", name)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("inventory count changed: got=%d want=%d assets=%v", len(got), len(want), got)
	}
}

func TestRunAdoptsExistingDataWithSnapshotAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	path := filepath.Join(dataDir, "notes.json")
	original := []byte(`{"version":1,"notes":[{"id":"n1"}]}`)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: []Asset{
		{Name: "notes", Path: path, CurrentVersion: 1, Critical: true, Validate: ValidateJSON},
	}}
	first, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Adopted || first.BackupDir == "" || len(first.Migrated) != 0 {
		t.Fatalf("unexpected first result: %+v", first)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("adoption must not rewrite user data")
	}
	if _, err := os.Stat(filepath.Join(first.BackupDir, "snapshot.json")); err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}

	second, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if second.Adopted || second.BackupDir != "" || len(second.Migrated) != 0 {
		t.Fatalf("second run is not idempotent: %+v", second)
	}
}

func TestRunMigratesOneAssetAndCommitsManifestLast(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	path := filepath.Join(dataDir, "state.json")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"value":"kept"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	base := Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: []Asset{
		{Name: "state", Path: path, CurrentVersion: 1, Critical: true, Validate: ValidateJSON},
	}}
	if _, err := Run(base); err != nil {
		t.Fatal(err)
	}
	base.ProductVersion = "v0.18"
	base.Assets[0].CurrentVersion = 2
	base.Assets[0].Migrations = map[int]Migration{1: func(raw []byte) ([]byte, error) {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		value["version"] = 2
		value["new_key"] = "default"
		return json.Marshal(value)
	}}
	result, err := Run(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Migrated) != 1 || result.Migrated[0] != "state" || result.BackupDir == "" {
		t.Fatalf("migration result: %+v", result)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"value":"kept"`) || !strings.Contains(string(raw), `"new_key":"default"`) {
		t.Fatalf("migration lost data: %s", raw)
	}
	manifest, exists, err := readManifest(filepath.Join(dataDir, manifestFileName))
	if err != nil || !exists || manifest.Assets["state"].Version != 2 || manifest.ProductVersion != "v0.18" {
		t.Fatalf("manifest not committed: exists=%v manifest=%+v err=%v", exists, manifest, err)
	}
}

func TestRunPreparationFailureLeavesEveryFileAndManifestUntouched(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	one := filepath.Join(dataDir, "one.json")
	two := filepath.Join(dataDir, "two.json")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(one, []byte(`{"version":1,"name":"one"}`), 0o600)
	_ = os.WriteFile(two, []byte(`{"version":1,"name":"two"}`), 0o600)
	assets := []Asset{
		{Name: "one", Path: one, CurrentVersion: 1, Critical: true, Validate: ValidateJSON},
		{Name: "two", Path: two, CurrentVersion: 1, Critical: true, Validate: ValidateJSON},
	}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v1", Now: fixedNow, Assets: assets}); err != nil {
		t.Fatal(err)
	}
	manifestBefore, _ := os.ReadFile(filepath.Join(dataDir, manifestFileName))
	oneBefore, _ := os.ReadFile(one)
	twoBefore, _ := os.ReadFile(two)
	for i := range assets {
		assets[i].CurrentVersion = 2
	}
	assets[0].Migrations = map[int]Migration{1: func([]byte) ([]byte, error) { return []byte(`{"version":2}`), nil }}
	assets[1].Migrations = map[int]Migration{1: func([]byte) ([]byte, error) { return nil, errors.New("injected") }}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v2", Now: fixedNow, Assets: assets}); err == nil {
		t.Fatal("expected migration failure")
	}
	oneAfter, _ := os.ReadFile(one)
	twoAfter, _ := os.ReadFile(two)
	manifestAfter, _ := os.ReadFile(filepath.Join(dataDir, manifestFileName))
	if string(oneAfter) != string(oneBefore) || string(twoAfter) != string(twoBefore) || string(manifestAfter) != string(manifestBefore) {
		t.Fatal("failed preparation changed durable state")
	}
}

func TestRunCommitFailureRollsBackAlreadyCommittedAssetsAndManifest(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	one := filepath.Join(dataDir, "one.json")
	two := filepath.Join(dataDir, "two.json")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	originalOne := []byte(`{"version":1,"name":"one"}`)
	originalTwo := []byte(`{"version":1,"name":"two"}`)
	if err := os.WriteFile(one, originalOne, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two, originalTwo, 0o600); err != nil {
		t.Fatal(err)
	}
	assets := []Asset{
		{Name: "one", Path: one, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: detectJSONVersion},
		{Name: "two", Path: two, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: detectJSONVersion},
	}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: assets}); err != nil {
		t.Fatal(err)
	}
	manifestBefore, err := os.ReadFile(filepath.Join(dataDir, manifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	for i := range assets {
		assets[i].CurrentVersion = 2
	}
	assets[0].Migrations = map[int]Migration{1: func(raw []byte) ([]byte, error) {
		return append(raw[:len(raw)-1], []byte(`,"new_key":"new-one"}`)...), nil
	}}
	assets[1].Migrations = map[int]Migration{1: func(raw []byte) ([]byte, error) {
		return append(raw[:len(raw)-1], []byte(`,"new_key":"new-two"}`)...), nil
	}}
	commitWrite := func(path string, raw []byte, mode fs.FileMode) error {
		if path == two {
			return errors.New("injected commit failure")
		}
		return writeAtomic(path, raw, mode)
	}
	if _, err := Run(Options{
		DataDir: dataDir, ProductVersion: "v0.18", Now: fixedNow, Assets: assets,
		writeAtomic: commitWrite,
	}); err == nil || !strings.Contains(err.Error(), "commit two") {
		t.Fatalf("expected injected commit failure, got %v", err)
	}
	afterOne, err := os.ReadFile(one)
	if err != nil {
		t.Fatal(err)
	}
	afterTwo, err := os.ReadFile(two)
	if err != nil {
		t.Fatal(err)
	}
	manifestAfter, err := os.ReadFile(filepath.Join(dataDir, manifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterOne) != string(originalOne) || string(afterTwo) != string(originalTwo) || string(manifestAfter) != string(manifestBefore) {
		t.Fatalf("commit failure did not restore all state: one=%s two=%s manifest=%s", afterOne, afterTwo, manifestAfter)
	}
}

func TestSequentialProductUpgradesRefuseDowngradeAndRestoreMatchingSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "state.json")
	original := []byte(`{"version":1,"user_value":"preserve"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	assets := []Asset{{
		Name: "state", Path: path, CurrentVersion: 1, Critical: true,
		Validate: ValidateJSON, DetectVersion: detectJSONVersion,
	}}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: assets}); err != nil {
		t.Fatal(err)
	}

	assets[0].CurrentVersion = 2
	assets[0].Migrations = map[int]Migration{1: func(raw []byte) ([]byte, error) {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		value["version"] = 2
		value["v18_key"] = "v18"
		return json.Marshal(value)
	}}
	v18, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.18", Now: fixedNow, Assets: assets})
	if err != nil {
		t.Fatal(err)
	}
	if v18.BackupDir == "" {
		t.Fatal("v0.18 migration must leave a restore point")
	}

	assets[0].CurrentVersion = 3
	assets[0].Migrations = map[int]Migration{2: func(raw []byte) ([]byte, error) {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		value["version"] = 3
		value["v19_key"] = "v19"
		return json.Marshal(value)
	}}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.19", Now: fixedNow, Assets: assets}); err != nil {
		t.Fatal(err)
	}
	beforeDowngrade, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.18", Now: fixedNow, Assets: assets}); err == nil || !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("v0.18 must refuse v0.19 data: %v", err)
	}
	afterDowngrade, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterDowngrade) != string(beforeDowngrade) {
		t.Fatal("rejected downgrade changed the v0.19 file")
	}

	if _, err := RestoreSnapshot(dataDir, v18.BackupDir, assets); err != nil {
		t.Fatal(err)
	}
	assets[0].CurrentVersion = 2
	assets[0].Migrations = map[int]Migration{1: func(raw []byte) ([]byte, error) {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		value["version"] = 2
		value["v18_key"] = "v18"
		return json.Marshal(value)
	}}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.18", Now: fixedNow, Assets: assets}); err != nil {
		t.Fatalf("restored v0.18 snapshot should be usable by v0.18: %v", err)
	}
	final, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(final, &value); err != nil {
		t.Fatal(err)
	}
	if value["version"] != float64(2) || value["user_value"] != "preserve" || value["v18_key"] != "v18" {
		t.Fatalf("restored v0.18 state is wrong: %s", final)
	}
	if _, ok := value["v19_key"]; ok {
		t.Fatalf("restored v0.18 state retained v0.19 field: %s", final)
	}
}

func TestRunRejectsNewerAssetVersion(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	path := filepath.Join(dataDir, "x.json")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path, []byte(`{}`), 0o600)
	manifest := Manifest{SchemaVersion: 1, Assets: map[string]AssetState{"x": {Version: 3}}}
	raw, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(dataDir, manifestFileName), raw, 0o600)
	_, err := Run(Options{DataDir: dataDir, ProductVersion: "old", Now: fixedNow, Assets: []Asset{
		{Name: "x", Path: path, CurrentVersion: 2, Critical: true, Validate: ValidateJSON},
	}})
	if err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("expected future-version rejection, got %v", err)
	}
}

func TestRunRejectsProductDowngradeBeforeChangingAnything(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "notes.json")
	original := []byte(`{"version":1,"notes":[{"future":"keep"}]}`)
	_ = os.WriteFile(path, original, 0o600)
	assets := []Asset{{Name: "notes", Path: path, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: detectJSONVersion}}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.19", Now: fixedNow, Assets: assets}); err != nil {
		t.Fatal(err)
	}
	manifestBefore, _ := os.ReadFile(filepath.Join(dataDir, manifestFileName))
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.18", Now: fixedNow, Assets: assets}); err == nil || !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("product downgrade must be rejected: %v", err)
	}
	after, _ := os.ReadFile(path)
	manifestAfter, _ := os.ReadFile(filepath.Join(dataDir, manifestFileName))
	if string(after) != string(original) || string(manifestAfter) != string(manifestBefore) {
		t.Fatal("rejected product downgrade changed durable state")
	}
}

func TestRunDetectsFutureVersionFromFileEvenWhenManifestIsStale(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "x.json")
	original := []byte(`{"version":3,"new_field":"must-survive"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{SchemaVersion: 1, Assets: map[string]AssetState{"x": {Version: 1}}}
	raw, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(dataDir, manifestFileName), raw, 0o600)
	_, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.18", Now: fixedNow, Assets: []Asset{{
		Name: "x", Path: path, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: detectJSONVersion,
	}}})
	if err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("future file must win over stale manifest: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("future-version rejection changed the file")
	}
}

func TestDefaultConfigAssetPerformsRealV1ToV2Upgrade(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	configPath := filepath.Join(root, "config.yaml")
	legacy := []byte(`schema_version: 1
app:
  host: 127.0.0.1
  data_dir: ./data
  download_dir: ./downloads
  log_dir: ./logs
systems:
  - name: production
    servers:
      - name: app-1
        host: 10.0.0.1
        auth_type: password
        log_dirs:
          - name: app
            path: /opt/app/logs
            patterns: ["*.log"]
`)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	asset := DefaultAssets(configPath, dataDir, filepath.Join(root, "downloads"))[0]
	result, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.18", Now: fixedNow, Assets: []Asset{asset}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Migrated) != 1 || result.BackupDir == "" {
		t.Fatalf("expected real config migration and snapshot: %+v", result)
	}
	after, _ := os.ReadFile(configPath)
	text := string(after)
	if !strings.Contains(text, "schema_version: 2") || !strings.Contains(text, "id: sys-") || !strings.Contains(text, "name: app-1") {
		t.Fatalf("config upgrade did not retain data/add IDs:\n%s", text)
	}
}

func TestConfigRemainsUntouchedWhenAnotherAssetCannotPrepare(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	configPath := filepath.Join(root, "config.yaml")
	legacyConfig := []byte("schema_version: 1\napp:\n  host: 127.0.0.1\n")
	brokenPath := filepath.Join(dataDir, "broken.json")
	_ = os.MkdirAll(dataDir, 0o700)
	_ = os.WriteFile(configPath, legacyConfig, 0o600)
	_ = os.WriteFile(brokenPath, []byte(`{"version":1}`), 0o600)
	configAsset := DefaultAssets(configPath, dataDir, filepath.Join(root, "downloads"))[0]
	broken := Asset{Name: "broken", Path: brokenPath, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: detectJSONVersion}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.18", Now: fixedNow, Assets: []Asset{configAsset, broken}}); err == nil {
		t.Fatal("missing migration should fail preparation")
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != string(legacyConfig) {
		t.Fatal("config was committed before all other assets prepared")
	}
}

func TestRunCriticalCorruptionBlocksButOptionalCorruptionWarns(t *testing.T) {
	for _, critical := range []bool{true, false} {
		t.Run(map[bool]string{true: "critical", false: "optional"}[critical], func(t *testing.T) {
			dataDir := t.TempDir()
			path := filepath.Join(dataDir, "bad.json")
			_ = os.WriteFile(path, []byte(`{broken`), 0o600)
			result, err := Run(Options{DataDir: dataDir, ProductVersion: "v1", Now: fixedNow, Assets: []Asset{
				{Name: "bad", Path: path, CurrentVersion: 1, Critical: critical, Validate: ValidateJSON},
			}})
			if critical {
				if err == nil {
					t.Fatal("critical corruption should block adoption")
				}
				return
			}
			if err != nil || len(result.Warnings) != 1 {
				t.Fatalf("optional corruption should warn: result=%+v err=%v", result, err)
			}
		})
	}
}

func TestRunRejectsStaleAndActiveLocks(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "stale"}[stale], func(t *testing.T) {
			dataDir := t.TempDir()
			lock := filepath.Join(dataDir, lockFileName)
			_ = os.WriteFile(lock, []byte("busy"), 0o600)
			_ = os.Chtimes(lock, fixedNow(), fixedNow())
			if stale {
				old := fixedNow().Add(-20 * time.Minute)
				_ = os.Chtimes(lock, old, old)
			}
			_, err := Run(Options{DataDir: dataDir, ProductVersion: "v1", Now: fixedNow})
			if stale && err == nil {
				t.Fatal("old lock must not be stolen")
			}
			if !stale && err == nil {
				t.Fatal("active lock should block")
			}
		})
	}
}

func TestRunRebuildsCorruptInternalManifestAfterSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "notes.json")
	_ = os.WriteFile(path, []byte(`{"version":1,"notes":[]}`), 0o600)
	manifestPath := filepath.Join(dataDir, manifestFileName)
	corrupt := []byte(`{broken`)
	_ = os.WriteFile(manifestPath, corrupt, 0o600)
	result, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.18", Now: fixedNow, Assets: []Asset{{
		Name: "notes", Path: path, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: detectJSONVersion,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) == 0 || result.BackupDir == "" {
		t.Fatalf("corrupt manifest recovery should warn and snapshot: %+v", result)
	}
	if _, _, err := readManifest(manifestPath); err != nil {
		t.Fatalf("manifest was not rebuilt: %v", err)
	}
	metaRaw, _ := os.ReadFile(filepath.Join(result.BackupDir, "snapshot.json"))
	if !strings.Contains(string(metaRaw), `"name": "upgrade-manifest"`) {
		t.Fatal("corrupt manifest was not included in recovery snapshot")
	}
}

func TestRestoreSnapshotVerifiesAndRestoresAllState(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	path := filepath.Join(dataDir, "notes.json")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"version":1,"notes":[{"id":"before"}]}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	assets := []Asset{{Name: "notes", Path: path, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: detectJSONVersion}}
	adopted, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: assets})
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(`{"version":1,"notes":[{"id":"after"}]}`)
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RestoreSnapshot(dataDir, adopted.BackupDir, assets)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatalf("snapshot did not restore original: %s", after)
	}
	if _, err := os.Stat(filepath.Join(dataDir, manifestFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("initial-adoption snapshot should restore missing manifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.SafetyBackupDir, "snapshot.json")); err != nil {
		t.Fatalf("pre-restore safety snapshot missing: %v", err)
	}
}

func TestRestoreSnapshotRejectsTamperingWithoutChangingCurrentFiles(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "notes.json")
	_ = os.WriteFile(path, []byte(`{"version":1,"notes":[]}`), 0o600)
	assets := []Asset{{Name: "notes", Path: path, CurrentVersion: 1, Critical: true, Validate: ValidateJSON}}
	adopted, err := Run(Options{DataDir: dataDir, ProductVersion: "v1", Now: fixedNow, Assets: assets})
	if err != nil {
		t.Fatal(err)
	}
	metaRaw, _ := os.ReadFile(filepath.Join(adopted.BackupDir, "snapshot.json"))
	var meta struct {
		Assets []snapshotEntry `json:"assets"`
	}
	_ = json.Unmarshal(metaRaw, &meta)
	for _, entry := range meta.Assets {
		if entry.Name == "notes" {
			_ = os.WriteFile(filepath.Join(adopted.BackupDir, entry.Backup), []byte(`tampered`), 0o600)
		}
	}
	current := []byte(`{"version":1,"notes":[{"id":"current"}]}`)
	_ = os.WriteFile(path, current, 0o600)
	if _, err := RestoreSnapshot(dataDir, adopted.BackupDir, assets); err == nil {
		t.Fatal("tampered snapshot must be rejected")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(current) {
		t.Fatal("rejected restore changed current data")
	}
}

// 自称 schema v2 但缺稳定 ID 的配置会被识别为逻辑 v1，
// 由协调器走同一个迁移步骤规范化补齐 ID。
func TestRunNormalizesV2ConfigMissingStableIDs(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	configPath := filepath.Join(root, "config.yaml")
	incomplete := []byte(`schema_version: 2
app:
  host: 127.0.0.1
  data_dir: ./data
  download_dir: ./downloads
  log_dir: ./logs
systems:
  - name: production
    servers:
      - name: app-1
        host: 10.0.0.1
        auth_type: password
        log_dirs:
          - name: app
            path: /opt/app/logs
            patterns: ["*.log"]
`) // 缺 systems 的稳定 ID
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, incomplete, 0o600); err != nil {
		t.Fatal(err)
	}
	asset := DefaultAssets(configPath, dataDir, filepath.Join(root, "downloads"))[0]
	result, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.18", Now: fixedNow, Assets: []Asset{asset}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Migrated) != 1 || result.BackupDir == "" {
		t.Fatalf("expected normalization migration and snapshot: %+v", result)
	}
	after, _ := os.ReadFile(configPath)
	text := string(after)
	if !strings.Contains(text, "schema_version: 2") || !strings.Contains(text, "id: sys-") {
		t.Fatalf("normalization did not add stable IDs:\n%s", text)
	}
}

// 空 JSON 文件与"业务字段恰好叫 version 且值不是整数"的文件都是合法的旧版形态：
// 升级器必须按 v1 对待（所有 store 也一致把空文件当无数据），既不阻断启动也不改写。
func TestRunTreatsEmptyAndNonIntegerVersionJSONAsLegacy(t *testing.T) {
	cases := map[string][]byte{
		"empty":            {},
		"whitespace":       []byte("  \n\t"),
		"string-version":   []byte(`{"version":"v2.1","theme":"dark"}`),
		"fractional":       []byte(`{"version":1.5,"theme":"dark"}`),
		"bare-array":       []byte(`[{"id":"legacy"}]`),
		"explicit-v0":      []byte(`{"version":0,"items":[]}`),
		"no-version-shell": []byte(`{"theme":"dark"}`),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			path := filepath.Join(dataDir, "browser_state.json")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: []Asset{
				{Name: "browser-state", Path: path, CurrentVersion: 1, Critical: false,
					Validate: ValidateJSON, DetectVersion: detectJSONVersion},
			}})
			if err != nil {
				t.Fatalf("legacy-shaped file must not block startup: %v", err)
			}
			if len(result.Warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", result.Warnings)
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(content) {
				t.Fatal("coordinator must not rewrite the file")
			}
		})
	}
}

// 空 JSON 文件即使注册为关键资产也不能阻断启动：ValidateJSON 明确放行空文件，
// 各 store 的空文件语义就是"无数据"，升级器不得比 store 更严格。
func TestRunAllowsEmptyCriticalFileAsNoData(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "notes.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: []Asset{
		{Name: "notes", Path: path, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: detectJSONVersion},
	}}); err != nil {
		t.Fatalf("empty critical file must read as no-data: %v", err)
	}
}

// 非关键文件的版本识别失败与校验失败同级：只警告，不阻断启动，也不改写文件。
func TestRunNonCriticalVersionDetectionFailureOnlyWarns(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "browser_state.json")
	original := []byte(`{"version":1,"tabs":[1,2,3]}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: []Asset{
		{Name: "browser-state", Path: path, CurrentVersion: 1, Critical: false,
			Validate:      ValidateJSON,
			DetectVersion: func([]byte) (int, error) { return 0, errors.New("detector exploded") }},
	}})
	if err != nil {
		t.Fatalf("non-critical detection failure must not block startup: %v", err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "detector exploded") {
		t.Fatalf("expected a single warning, got %v", result.Warnings)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("coordinator must not rewrite the file")
	}
}

// 陈旧锁被接管后，原持有者的 release 绝不能删掉接管者的锁，
// 否则第三个事务就能与当前事务并发执行。
func TestLockReleaseNeverDeletesForeignLock(t *testing.T) {
	dataDir := t.TempDir()
	lock := filepath.Join(dataDir, lockFileName)
	stale := fixedNow().Add(-20 * time.Minute)
	releaseA, err := acquireLock(lock, stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(lock, stale, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(lock, fixedNow()); err == nil {
		t.Fatal("live old lock was stolen")
	}
	// Simulate external replacement to verify the release ownership check.
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	releaseB, err := acquireLock(lock, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	releaseA() // A 恢复运行并结束
	if _, statErr := os.Stat(lock); statErr != nil {
		t.Fatalf("stale holder's release deleted the new owner's lock: %v", statErr)
	}
	releaseB()
	if _, statErr := os.Stat(lock); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatal("owner release must remove its own lock")
	}
}

// 无任何变化的重复启动不得重写 upgrade-state.json（updated_at 不应抖动）；
// 但仅产品版本变化时必须写入清单——降级保护依赖最新产品版本。
func TestIdempotentRunSkipsManifestRewrite(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "notes.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"notes":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	assets := []Asset{{Name: "notes", Path: path, CurrentVersion: 1, Critical: true, Validate: ValidateJSON}}
	manifestPath := filepath.Join(dataDir, manifestFileName)
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: assets}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	later := fixedNow().Add(2 * time.Hour)
	nowLater := func() time.Time { return later }
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: nowLater, Assets: assets}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(manifestPath)
	if string(before) != string(after) {
		t.Fatal("no-op startup rewrote the manifest")
	}
	third, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.18", Now: nowLater, Assets: assets})
	if err != nil {
		t.Fatal(err)
	}
	if third.Current.ProductVersion != "v0.18" {
		t.Fatalf("product version bump must be recorded: %+v", third.Current)
	}
	raw, _ := os.ReadFile(manifestPath)
	if !strings.Contains(string(raw), `"product_version": "v0.18"`) {
		t.Fatal("manifest on disk was not updated with the new product version")
	}
}

// 配置迁移链必须满足协调器契约：每个迁移器只把文档精确推进一个版本。
// 新增 schema 版本时该测试会自动覆盖新的迁移步骤，防止全量迁移混进迁移链。
func TestConfigMigrationsAdvanceExactlyOneVersionEach(t *testing.T) {
	asset := DefaultAssets(filepath.Join(t.TempDir(), "config.yaml"), t.TempDir(), t.TempDir())[0]
	doc := []byte(`app:
  host: 127.0.0.1
  data_dir: ./data
  download_dir: ./downloads
  log_dir: ./logs
systems:
  - name: production
    servers:
      - name: app-1
        host: 10.0.0.1
        auth_type: password
        log_dirs:
          - name: app
            path: /opt/app/logs
            patterns: ["*.log"]
`) // 无 schema_version 的 v0 历史文档
	for from := 0; from < config.CurrentSchemaVersion; from++ {
		migration, ok := asset.Migrations[from]
		if !ok {
			t.Fatalf("migration for version %d is missing", from)
		}
		next, err := migration(doc)
		if err != nil {
			t.Fatalf("migration %d failed: %v", from, err)
		}
		version, err := config.DetectDocumentVersion(next)
		if err != nil {
			t.Fatal(err)
		}
		if version != from+1 {
			t.Fatalf("migration %d advanced the document to version %d, want exactly %d", from, version, from+1)
		}
		doc = next
	}
}
