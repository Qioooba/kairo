package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"kairo/internal/config"
)

func TestEmbeddedBootstrapConfig(t *testing.T) {
	bootstrap, err := config.BootstrapDistributionConfig(bootstrapConfigFile)
	if err != nil {
		t.Fatalf("内置配置无效: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	result, err := config.Open(path, bootstrap)
	if err != nil {
		t.Fatalf("创建首次启动配置失败: %v", err)
	}
	cfg := result.Config
	if cfg.SchemaVersion != config.CurrentSchemaVersion || len(cfg.Systems) == 0 {
		t.Fatalf("内置配置不完整: schema=%d systems=%d", cfg.SchemaVersion, len(cfg.Systems))
	}
	if cfg.App.KairoInternalToken != "" || cfg.InternalEndpoints.SponsorLeaderboard.Primary != "" {
		t.Fatal("发行配置不应包含开发者旁路或本机端点覆盖")
	}
}

func TestExeDirectory(t *testing.T) {
	dir, err := exeDirectory()
	if err != nil {
		t.Fatalf("exeDirectory: %v", err)
	}
	if dir == "" {
		t.Fatal("empty dir")
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("not absolute: %q", dir)
	}
	// 必须是已存在的目录
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dir not exist: %v", err)
	}
	// 当前进程的可执行文件应在这个目录里（或其 EvalSymlinks 结果里）
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows 上不解析符号链接，直接比 basename 父目录
		if filepath.Dir(exe) != dir {
			t.Errorf("exe=%q, dir=%q, mismatch on windows", exe, dir)
		}
	} else {
		// 其它平台：解析 symlink
		real, _ := filepath.EvalSymlinks(exe)
		if real != "" && filepath.Dir(real) != dir {
			t.Errorf("exe real=%q, dir=%q, mismatch", real, dir)
		}
	}
}
