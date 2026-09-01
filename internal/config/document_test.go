package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesSingleConfigFromBootstrap(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "config.yaml")
	result, err := Open(path, []byte(validYAML))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !result.Created || result.Config.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("created=%v schema=%d", result.Created, result.Config.SchemaVersion)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "schema_version: 2") {
		t.Fatalf("首次启动配置未写 schema_version:\n%s", raw)
	}
}

func TestOpenMigratesAndBacksUpLegacyConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !result.Migrated || result.Backup == "" {
		t.Fatalf("migrated=%v backup=%q", result.Migrated, result.Backup)
	}
	backup, err := os.ReadFile(result.Backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != validYAML {
		t.Fatal("迁移备份没有保留原始配置")
	}
	sys := result.Config.Systems[0]
	if sys.ID == "" || sys.Servers[0].ID == "" || sys.Servers[0].LogDirs[0].ID == "" {
		t.Fatalf("v2 migration did not assign stable IDs: %+v", sys)
	}
	reopened, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Migrated || reopened.Config.Systems[0].ID != sys.ID || reopened.Config.Systems[0].Servers[0].ID != sys.Servers[0].ID {
		t.Fatal("stable IDs changed on idempotent reopen")
	}
}

func TestPrepareForUpgradeDoesNotCommitExistingMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte(validYAML)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareForUpgrade(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.Migrated || prepared.Config.SchemaVersion != CurrentSchemaVersion || prepared.Backup != "" {
		t.Fatalf("unexpected prepared result: %+v", prepared)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("prepare phase committed config before cross-file transaction")
	}
}

func TestOpenRejectsNewerSchemaWithoutChangingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	raw := []byte("schema_version: 999\napp:\n  host: 127.0.0.1\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, nil); err == nil || !strings.Contains(err.Error(), "高于当前程序") {
		t.Fatalf("应拒绝未来配置版本，得到: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(raw) {
		t.Fatal("拒绝未来版本时不应改写配置")
	}
}

func TestBootstrapDistributionConfigKeepsServersButRemovesDeveloperOverrides(t *testing.T) {
	raw := []byte(validYAML + "\ninternal_endpoints:\n  sponsor_leaderboard:\n    primary: http://127.0.0.1:18093\n")
	out, err := BootstrapDistributionConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "mock-1") {
		t.Fatal("发行模板应保留预置服务器")
	}
	if strings.Contains(text, "18093") {
		t.Fatal("发行模板不应保留开发者内部端点覆盖")
	}
}

func TestResetToDistributionOverwritesServersButPreservesUserDataRouting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	user := "schema_version: 2\n" + strings.Replace(validYAML, "mock-1", "user-server", 1)
	user = strings.Replace(user, "  download_dir: ./downloads", "  download_dir: ./my-downloads", 1)
	user = strings.Replace(user, "  data_dir: ./data\n", "  data_dir: ./my-data\n  credential_store: file\n  credential_key: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n", 1)
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "my-data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	petPath := filepath.Join(dataDir, "pet.json")
	petRaw := []byte(`{"v":1,"payload":"user-pet"}`)
	if err := os.WriteFile(petPath, petRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	backup, err := ResetToDistribution(path, []byte(validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("reset must back up the previous config")
	}
	reset, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Systems[0].Servers[0].Name != "mock-1" {
		t.Fatalf("official servers were not restored: %+v", reset.Systems)
	}
	if reset.App.DataDir != "./my-data" || reset.App.DownloadDir != "./my-downloads" || reset.App.CredentialStore != "file" || reset.App.CredentialKey == "" {
		t.Fatalf("durable data routing was not preserved: %+v", reset.App)
	}
	afterPet, err := os.ReadFile(petPath)
	if err != nil || string(afterPet) != string(petRaw) {
		t.Fatalf("reset touched pet data: %q err=%v", afterPet, err)
	}
}

func TestDefaultsKeepsOriginalIDsAndRekeysCopiedEntities(t *testing.T) {
	cfg, _, err := decodeAndMigrate([]byte(validYAML))
	if err != nil {
		t.Fatal(err)
	}
	original := cfg.Systems[0]
	copyInput := original
	copyInput.Servers = append([]ServerConfig(nil), original.Servers...)
	copyInput.Servers[0].LogDirs = append([]LogDirEntry(nil), original.Servers[0].LogDirs...)
	cfg.Systems = append(cfg.Systems, copyInput) // 模拟前端 JSON 深拷贝时连 ID 一起复制。
	cfg.Defaults()
	copy := cfg.Systems[1]
	if cfg.Systems[0].ID != original.ID {
		t.Fatal("original stable ID changed")
	}
	if copy.ID == original.ID || copy.Servers[0].ID == original.Servers[0].ID || copy.Servers[0].LogDirs[0].ID == original.Servers[0].LogDirs[0].ID {
		t.Fatalf("copied entities kept duplicate IDs: original=%+v copy=%+v", original, copy)
	}
}
