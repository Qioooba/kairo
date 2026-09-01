package upgrade

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"

	"kairo/internal/config"
)

// DefaultAssets is the complete inventory of user-owned durable files.
// Generated caches, logs, temporary edits and downloaded payloads are deliberately excluded.
func DefaultAssets(configPath, dataDir, downloadDir string) []Asset {
	jsonFiles := []struct {
		name     string
		filename string
		critical bool
	}{
		{"preferences", "preferences.json", true},
		{"credentials", "credentials.json", true},
		{"database-sources", "database-sources.json", true},
		{"pet", "pet.json", true},
		{"browser-state", "browser_state.json", false},
		{"ssh-compat-profiles", "ssh_compat_profiles.json", false},
		{"reminders", "reminders.json", true},
		{"notes", "notes.json", true},
		{"scheduled-tasks", "sched_tasks.json", true},
		{"scheduled-task-runs", "sched_task_runs.json", true},
		{"http-cases", "http_cases.json", true},
		{"wsdl-projects", "wsdl_projects.json", true},
		{"soap-templates", "soap_templates.json", true},
		{"soap-history", "soap_history.json", false},
		{"soap-mocks", "soap_mocks.json", true},
		{"soap-mock-records", "soap_mock_records.json", false},
		{"desktop-pet-preference", "deskpet.json", false},
	}
	// 每个迁移器只负责推进一个版本（N -> N+1），与协调器的逐版本迁移链契约一致。
	configSteps := map[int]Migration{}
	for from := 0; from < config.CurrentSchemaVersion; from++ {
		configSteps[from] = config.UpgradeYAMLFrom(from)
	}
	assets := []Asset{{
		Name: "config", Path: configPath, CurrentVersion: config.CurrentSchemaVersion,
		Critical: true, Validate: ValidateYAML, DetectVersion: config.DetectDocumentVersion,
		Migrations: configSteps,
	}}
	for _, item := range jsonFiles {
		assets = append(assets, Asset{
			Name: item.name, Path: filepath.Join(dataDir, item.filename), CurrentVersion: 1,
			Critical: item.critical, Validate: ValidateJSON, DetectVersion: detectJSONVersion,
		})
	}
	assets = append(assets,
		Asset{Name: "credential-key", Path: filepath.Join(dataDir, ".credkey"), CurrentVersion: 1, Critical: true, Validate: ValidateHexKey, DetectVersion: fixedVersion(1)},
		Asset{Name: "pet-key", Path: filepath.Join(dataDir, ".petkey"), CurrentVersion: 1, Critical: true, Validate: ValidateHexKey, DetectVersion: fixedVersion(1)},
		Asset{Name: "download-metadata", Path: filepath.Join(downloadDir, ".kairo-meta.json"), CurrentVersion: 1, Critical: false, Validate: ValidateJSON, DetectVersion: detectJSONVersion},
	)
	if home, err := os.UserHomeDir(); err == nil {
		assets = append(assets, Asset{Name: "license-certificate", Path: filepath.Join(home, ".kairo", "license.dat"), CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: detectJSONVersion})
	}
	return assets
}

func fixedVersion(version int) VersionDetector {
	return func([]byte) (int, error) { return version, nil }
}

// detectJSONVersion accepts both legacy JSON (array/object without a version)
// and the current versioned envelope. Pet state historically used "v".
// 空文件与 ValidateJSON 的放行语义保持一致：按旧版"无数据"对待，不阻断启动。
func detectJSONVersion(raw []byte) (int, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return 1, nil
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return 0, err
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return 1, nil
	}
	for _, key := range []string{"version", "schema_version", "v"} {
		value, exists := obj[key]
		if !exists {
			continue
		}
		number, ok := value.(float64)
		// 业务字段可能恰好叫 version；只要值不是整数（字符串、小数），就不当作格式版本。
		if !ok || number != math.Trunc(number) {
			return 1, nil
		}
		if int(number) == 0 { // v0 means the pre-versioned legacy representation.
			return 1, nil
		}
		return int(number), nil
	}
	return 1, nil
}
