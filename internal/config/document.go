package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// CurrentSchemaVersion 是当前程序能够完整读写的配置格式版本。
// 它只在配置结构需要迁移时递增，不跟随产品版本递增。
const CurrentSchemaVersion = 1

// LoadResult 描述一次配置打开操作。Created/Migrated 主要供启动日志和测试使用。
type LoadResult struct {
	Config   *Config
	Created  bool
	Migrated bool
	Backup   string
}

// Open 打开唯一的用户配置文件。文件不存在时从 bootstrap 创建；旧格式在备份后原子迁移。
func Open(path string, bootstrap []byte) (*LoadResult, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析配置路径失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建配置目录失败: %w", err)
	}

	raw, err := os.ReadFile(path)
	created := false
	if errors.Is(err, fs.ErrNotExist) {
		if len(bootstrap) == 0 {
			return nil, fmt.Errorf("配置文件不存在且没有首次启动模板: %s", path)
		}
		raw = bootstrap
		created = true
	} else if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	cfg, migrated, err := decodeAndMigrate(raw)
	if err != nil {
		return nil, err
	}

	result := &LoadResult{Config: cfg, Created: created, Migrated: migrated}
	if created {
		if err := writeYAMLAtomic(path, cfg); err != nil {
			return nil, fmt.Errorf("创建首次启动配置失败: %w", err)
		}
		return result, nil
	}
	if migrated {
		backup, err := backupPath(path)
		if err != nil {
			return nil, fmt.Errorf("迁移前备份配置失败: %w", err)
		}
		if err := writeYAMLAtomic(path, cfg); err != nil {
			return nil, fmt.Errorf("写入迁移后配置失败: %w", err)
		}
		result.Backup = backup
	}
	return result, nil
}

func decodeAndMigrate(raw []byte) (*Config, bool, error) {
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, false, fmt.Errorf("解析 yaml 失败: %w", err)
	}
	if cfg.SchemaVersion < 0 {
		return nil, false, fmt.Errorf("schema_version 不能为负数: %d", cfg.SchemaVersion)
	}
	if cfg.SchemaVersion > CurrentSchemaVersion {
		return nil, false, fmt.Errorf("配置格式版本 %d 高于当前程序支持的 %d，请升级程序后再打开", cfg.SchemaVersion, CurrentSchemaVersion)
	}

	migrated := false
	for cfg.SchemaVersion < CurrentSchemaVersion {
		switch cfg.SchemaVersion {
		case 0:
			// v0 是历史无版本格式。v1 只建立正式版本边界，字段语义保持不变。
			cfg.SchemaVersion = 1
		default:
			return nil, false, fmt.Errorf("没有从配置版本 %d 开始的迁移器", cfg.SchemaVersion)
		}
		migrated = true
	}

	cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, false, err
	}
	if err := cfg.Auth.Prepare(); err != nil {
		return nil, false, err
	}
	return &cfg, migrated, nil
}

// BootstrapDistributionConfig 把仓库内的预置配置转成首次启动模板。
// 明文服务器和密码按产品约定保留，仅移除明确标注为开发者本机旁路的字段。
func BootstrapDistributionConfig(raw []byte) ([]byte, error) {
	cfg, _, err := decodeAndMigrate(raw)
	if err != nil {
		return nil, err
	}
	cfg.App.KairoInternalToken = ""
	cfg.InternalEndpoints = InternalEndpointsConfig{}
	return yaml.Marshal(cfg)
}

func backupPath(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	backupDir := filepath.Join(filepath.Dir(path), "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("config-%s.yaml", time.Now().Format("20060102-150405.000000000"))
	backup := filepath.Join(backupDir, name)
	if err := os.WriteFile(backup, raw, 0o600); err != nil {
		return "", err
	}
	return backup, nil
}
