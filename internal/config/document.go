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
const CurrentSchemaVersion = 2

type configMigration func(*Config) error

var configMigrations = map[int]configMigration{
	0: func(cfg *Config) error {
		// v0 是历史无版本格式。v1 建立正式版本边界，字段语义保持不变。
		cfg.SchemaVersion = 1
		return nil
	},
	1: func(cfg *Config) error {
		// v2 为系统、服务器和日志目录补稳定 ID，给后续定向升级提供锚点。
		cfg.ensureStableIDs()
		cfg.SchemaVersion = 2
		return nil
	},
}

// LoadResult 描述一次配置打开操作。Created/Migrated 主要供启动日志和测试使用。
type LoadResult struct {
	Config   *Config
	Created  bool
	Migrated bool
	Backup   string
}

// ResetToDistribution 是用户明确要求“恢复官方配置”时的覆盖入口。
// 它会备份旧配置并替换服务器等配置，但保留数据目录位置和 file 凭据密钥，
// 因而宠物、便笺、提醒、任务和已保存凭据不会因重置而失联。
func ResetToDistribution(path string, bootstrap []byte) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析配置路径失败: %w", err)
	}
	replacement, _, err := decodeAndMigrate(bootstrap)
	if err != nil {
		return "", fmt.Errorf("发行配置无效: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("创建配置目录失败: %w", err)
	}

	backup := ""
	if raw, readErr := os.ReadFile(path); readErr == nil {
		// 只提取必须保留的数据寻址字段；即使旧文件来自未来版本，也不解释或回写其余字段。
		var old struct {
			App struct {
				DownloadDir     string `yaml:"download_dir"`
				LogDir          string `yaml:"log_dir"`
				DataDir         string `yaml:"data_dir"`
				CredentialStore string `yaml:"credential_store"`
				CredentialKey   string `yaml:"credential_key"`
			} `yaml:"app"`
		}
		if yaml.Unmarshal(raw, &old) == nil {
			if old.App.DownloadDir != "" {
				replacement.App.DownloadDir = old.App.DownloadDir
			}
			if old.App.LogDir != "" {
				replacement.App.LogDir = old.App.LogDir
			}
			if old.App.DataDir != "" {
				replacement.App.DataDir = old.App.DataDir
			}
			if old.App.CredentialStore != "" {
				replacement.App.CredentialStore = old.App.CredentialStore
			}
			if old.App.CredentialKey != "" {
				replacement.App.CredentialKey = old.App.CredentialKey
			}
		}
		backup, err = backupPath(path)
		if err != nil {
			return "", fmt.Errorf("重置前备份配置失败: %w", err)
		}
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return "", fmt.Errorf("读取待重置配置失败: %w", readErr)
	}
	if err := writeYAMLAtomic(path, replacement); err != nil {
		return "", fmt.Errorf("写入发行配置失败: %w", err)
	}
	return backup, nil
}

// Open 打开唯一的用户配置文件。文件不存在时从 bootstrap 创建；旧格式在备份后原子迁移。
func Open(path string, bootstrap []byte) (*LoadResult, error) {
	return open(path, bootstrap, true)
}

// PrepareForUpgrade 读取并校验配置，但把已有文件的迁移仅保留在内存中。
// 启动流程用它先定位 dataDir，再交给跨文件升级协调器统一备份和提交。
// 首次启动没有旧文件可保护，仍会立即创建发行配置。
func PrepareForUpgrade(path string, bootstrap []byte) (*LoadResult, error) {
	return open(path, bootstrap, false)
}

func open(path string, bootstrap []byte, commitMigration bool) (*LoadResult, error) {
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
	if migrated && commitMigration {
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

// DetectDocumentVersion returns the logical migration version. A schema-v2
// document missing required stable IDs is deliberately reported as v1 so the
// coordinator normalizes it under the same transaction and snapshot.
func DetectDocumentVersion(raw []byte) (int, error) {
	var header struct {
		SchemaVersion int `yaml:"schema_version"`
	}
	if err := yaml.Unmarshal(raw, &header); err != nil {
		return 0, err
	}
	_, migrated, err := decodeAndMigrate(raw)
	if err != nil {
		return 0, err
	}
	if header.SchemaVersion >= CurrentSchemaVersion && migrated {
		return CurrentSchemaVersion - 1, nil
	}
	if header.SchemaVersion == 0 {
		return 1, nil
	}
	return header.SchemaVersion, nil
}

func decodeDocument(raw []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析 yaml 失败: %w", err)
	}
	if cfg.SchemaVersion < 0 {
		return nil, fmt.Errorf("schema_version 不能为负数: %d", cfg.SchemaVersion)
	}
	if cfg.SchemaVersion > CurrentSchemaVersion {
		return nil, fmt.Errorf("配置格式版本 %d 高于当前程序支持的 %d，请升级程序后再打开", cfg.SchemaVersion, CurrentSchemaVersion)
	}
	return &cfg, nil
}

func decodeAndMigrate(raw []byte) (*Config, bool, error) {
	cfg, err := decodeDocument(raw)
	if err != nil {
		return nil, false, err
	}

	migrated := cfg.SchemaVersion >= 2 && !cfg.stableIDsComplete()
	for cfg.SchemaVersion < CurrentSchemaVersion {
		migration := configMigrations[cfg.SchemaVersion]
		if migration == nil {
			return nil, false, fmt.Errorf("没有从配置版本 %d 开始的迁移器", cfg.SchemaVersion)
		}
		if err := migration(cfg); err != nil {
			return nil, false, fmt.Errorf("配置从版本 %d 迁移失败: %w", cfg.SchemaVersion, err)
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
	return cfg, migrated, nil
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

// UpgradeYAMLFrom 返回把配置文档从版本 from 精确推进到 from+1 的迁移函数，
// 供跨文件升级协调器逐版本调用。全量迁移到最新版本只允许发生在进程内打开
// 配置时（decodeAndMigrate），绝不能出现在逐版本迁移链里——否则新增 schema
// 版本后，协调器的版本记账会和文档实际版本错位。
// 输入文档可能是"自称 v2 但缺稳定 ID"的逻辑 v1 文档（见 DetectDocumentVersion），
// 此时 from 为其逻辑版本，应用对应迁移器即可完成规范化。
func UpgradeYAMLFrom(from int) func(raw []byte) ([]byte, error) {
	return func(raw []byte) ([]byte, error) {
		cfg, err := decodeDocument(raw)
		if err != nil {
			return nil, err
		}
		migration := configMigrations[from]
		if migration == nil {
			return nil, fmt.Errorf("没有从配置版本 %d 开始的迁移器", from)
		}
		if err := migration(cfg); err != nil {
			return nil, fmt.Errorf("配置从版本 %d 迁移失败: %w", from, err)
		}
		cfg.Defaults()
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		if err := cfg.Auth.Prepare(); err != nil {
			return nil, err
		}
		return yaml.Marshal(cfg)
	}
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
