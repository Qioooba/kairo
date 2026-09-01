package upgrade

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RestoreResult struct {
	SafetyBackupDir string
}

// RestoreSnapshot restores a verified upgrade snapshot. It first snapshots the
// current files, so a failed or mistaken restore is itself recoverable.
func RestoreSnapshot(dataDir, backupDir string, assets []Asset) (*RestoreResult, error) {
	if err := validateAssets(assets); err != nil {
		return nil, err
	}
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	backupDir, err = filepath.Abs(backupDir)
	if err != nil {
		return nil, err
	}
	backupRoot := filepath.Join(dataDir, "backups", "upgrades")
	rel, err := filepath.Rel(backupRoot, backupDir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("upgrade: restore snapshot must be inside the managed backup directory")
	}
	release, err := acquireLock(filepath.Join(dataDir, lockFileName), time.Now())
	if err != nil {
		return nil, err
	}
	defer release()

	metaRaw, err := os.ReadFile(filepath.Join(backupDir, "snapshot.json"))
	if err != nil {
		return nil, fmt.Errorf("upgrade: read restore snapshot: %w", err)
	}
	var meta struct {
		Assets []snapshotEntry `json:"assets"`
	}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return nil, fmt.Errorf("upgrade: parse restore snapshot: %w", err)
	}
	allowed := map[string]string{"upgrade-manifest": filepath.Join(dataDir, manifestFileName)}
	for _, asset := range assets {
		allowed[asset.Name] = asset.Path
	}
	seen := map[string]bool{}
	for _, entry := range meta.Assets {
		wantPath, ok := allowed[entry.Name]
		if !ok || seen[entry.Name] || filepath.Clean(entry.Path) != filepath.Clean(wantPath) {
			return nil, fmt.Errorf("upgrade: snapshot contains unknown or mismatched asset %q", entry.Name)
		}
		seen[entry.Name] = true
		if !entry.Existed {
			continue
		}
		if entry.Backup == "" || filepath.Base(entry.Backup) != entry.Backup {
			return nil, fmt.Errorf("upgrade: invalid backup name for %s", entry.Name)
		}
		raw, err := readManagedFile(filepath.Join(backupDir, entry.Backup))
		if err != nil {
			return nil, fmt.Errorf("upgrade: read backup %s: %w", entry.Name, err)
		}
		if entry.SHA256 == "" || digest(raw) != entry.SHA256 {
			return nil, fmt.Errorf("upgrade: backup checksum mismatch for %s", entry.Name)
		}
	}
	// A file introduced after the chosen snapshot did not exist at that point.
	// Include it as Existed=false so a full point-in-time restore cannot leave a
	// half-old/half-new mixture behind.
	for name, path := range allowed {
		if !seen[name] {
			meta.Assets = append(meta.Assets, snapshotEntry{Name: name, Path: path})
		}
	}

	current := make([]preparedAsset, 0, len(meta.Assets))
	for _, entry := range meta.Assets {
		current = append(current, preparedAsset{asset: Asset{Name: entry.Name, Path: entry.Path}})
	}
	safety, err := createSnapshot(dataDir, current, time.Now())
	if err != nil {
		return nil, fmt.Errorf("upgrade: create pre-restore safety snapshot: %w", err)
	}
	target := &snapshot{dir: backupDir, entries: meta.Assets}
	if err := target.restore(); err != nil {
		if rollbackErr := safety.restore(); rollbackErr != nil {
			return nil, fmt.Errorf("upgrade: restore failed: %v; safety rollback failed: %w", err, rollbackErr)
		}
		return nil, fmt.Errorf("upgrade: restore failed and current state was recovered: %w", err)
	}
	return &RestoreResult{SafetyBackupDir: safety.dir}, nil
}
