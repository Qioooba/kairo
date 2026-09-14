package upgrade

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type snapshotEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Backup  string `json:"backup,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Existed bool   `json:"existed"`
	Mode    uint32 `json:"mode,omitempty"`
}

type snapshot struct {
	dir     string
	entries []snapshotEntry
}

func createSnapshot(dataDir string, assets []preparedAsset, now time.Time) (*snapshot, error) {
	root := filepath.Join(dataDir, "backups", "upgrades")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("upgrade: create backup root: %w", err)
	}
	base := "upgrade-" + now.Format("20060102-150405.000000000")
	dir := filepath.Join(root, base)
	for suffix := 0; ; suffix++ {
		if suffix > 0 {
			dir = filepath.Join(root, fmt.Sprintf("%s-%d", base, suffix))
		}
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || suffix >= 1000 {
			return nil, fmt.Errorf("upgrade: create snapshot: %w", err)
		}
	}
	s := &snapshot{dir: dir, entries: make([]snapshotEntry, 0, len(assets))}
	manifestIncluded := false
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dir)
		}
	}()
	for i, item := range assets {
		if item.asset.Name == "upgrade-manifest" {
			manifestIncluded = true
		}
		entry := snapshotEntry{Name: item.asset.Name, Path: item.asset.Path}
		info, err := os.Lstat(item.asset.Path)
		if errors.Is(err, fs.ErrNotExist) {
			s.entries = append(s.entries, entry)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("upgrade: stat %s for backup: %w", item.asset.Name, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("upgrade: %s is not a regular file", item.asset.Name)
		}
		entry.Existed = true
		entry.Mode = uint32(info.Mode().Perm())
		entry.Backup = fmt.Sprintf("%03d-%s", i, filepath.Base(item.asset.Path))
		backupPath := filepath.Join(dir, entry.Backup)
		if err := copyRegular(item.asset.Path, backupPath, info.Mode().Perm()); err != nil {
			return nil, fmt.Errorf("upgrade: backup %s: %w", item.asset.Name, err)
		}
		backupRaw, err := os.ReadFile(backupPath)
		if err != nil {
			return nil, fmt.Errorf("upgrade: verify backup %s: %w", item.asset.Name, err)
		}
		entry.SHA256 = digest(backupRaw)
		s.entries = append(s.entries, entry)
	}
	// The manifest is part of the transaction boundary too. Restoring a snapshot
	// must restore the exact asset-version view that existed before the upgrade.
	manifestPath := filepath.Join(dataDir, manifestFileName)
	manifestPrepared := preparedAsset{asset: Asset{Name: "upgrade-manifest", Path: manifestPath}}
	if !manifestIncluded {
		if info, err := os.Lstat(manifestPath); err == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("upgrade: manifest is not a regular file")
			}
			entry := snapshotEntry{Name: manifestPrepared.asset.Name, Path: manifestPath, Existed: true, Mode: uint32(info.Mode().Perm()), Backup: fmt.Sprintf("%03d-%s", len(s.entries), manifestFileName)}
			backupPath := filepath.Join(dir, entry.Backup)
			if err := copyRegular(manifestPath, backupPath, info.Mode().Perm()); err != nil {
				return nil, fmt.Errorf("upgrade: backup manifest: %w", err)
			}
			raw, readErr := os.ReadFile(backupPath)
			if readErr != nil {
				return nil, fmt.Errorf("upgrade: verify manifest backup: %w", readErr)
			}
			entry.SHA256 = digest(raw)
			s.entries = append(s.entries, entry)
		} else if errors.Is(err, fs.ErrNotExist) {
			s.entries = append(s.entries, snapshotEntry{Name: manifestPrepared.asset.Name, Path: manifestPath})
		} else {
			return nil, fmt.Errorf("upgrade: stat manifest for backup: %w", err)
		}
	}
	meta, err := json.MarshalIndent(map[string]any{
		"created_at": now.UTC().Format(time.RFC3339Nano),
		"assets":     s.entries,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), meta, 0o600); err != nil {
		return nil, fmt.Errorf("upgrade: write snapshot manifest: %w", err)
	}
	ok = true
	return s, nil
}

func loadSnapshot(dir string) (*snapshot, error) {
	metaRaw, err := os.ReadFile(filepath.Join(dir, "snapshot.json"))
	if err != nil {
		return nil, fmt.Errorf("upgrade: read snapshot manifest: %w", err)
	}
	var meta struct {
		Assets []snapshotEntry `json:"assets"`
	}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return nil, fmt.Errorf("upgrade: parse snapshot manifest: %w", err)
	}
	return &snapshot{dir: dir, entries: meta.Assets}, nil
}

func (s *snapshot) verify() error {
	for _, entry := range s.entries {
		if !entry.Existed {
			continue
		}
		if entry.Backup == "" {
			return fmt.Errorf("upgrade: snapshot entry %s missing backup filename", entry.Name)
		}
		backupPath := filepath.Join(s.dir, entry.Backup)
		raw, err := os.ReadFile(backupPath)
		if err != nil {
			return fmt.Errorf("read backup %s (%s): %w", entry.Name, backupPath, err)
		}
		if entry.SHA256 != "" && digest(raw) != entry.SHA256 {
			return fmt.Errorf("corrupted backup %s: expected checksum %s, got %s", entry.Name, entry.SHA256, digest(raw))
		}
	}
	return nil
}

func (s *snapshot) restore() error {
	if err := s.verify(); err != nil {
		return fmt.Errorf("upgrade: verify snapshot before restore: %w", err)
	}
	var errs []error
	for _, entry := range s.entries {
		if !entry.Existed {
			if err := os.Remove(entry.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("remove newly-created %s: %w", entry.Name, err))
			}
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, entry.Backup))
		if err != nil {
			errs = append(errs, fmt.Errorf("read backup %s: %w", entry.Name, err))
			continue
		}
		mode := fs.FileMode(entry.Mode)
		if mode == 0 {
			mode = 0o600
		}
		if err := writeAtomic(entry.Path, raw, mode); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", entry.Name, err))
		}
	}
	return errors.Join(errs...)
}

