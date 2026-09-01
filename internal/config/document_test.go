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
	if !strings.Contains(string(raw), "schema_version: 1") {
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
