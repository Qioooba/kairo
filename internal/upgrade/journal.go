package upgrade

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type JournalPhase string

const (
	PhaseIdle              JournalPhase = "idle"
	PhasePrepared          JournalPhase = "prepared"
	PhaseApplying          JournalPhase = "applying"
	PhaseManifestCommitted JournalPhase = "manifest_committed"
	PhaseComplete          JournalPhase = "complete"
	PhaseRecovering        JournalPhase = "recovering"
	PhaseRestored          JournalPhase = "restored"
	PhaseRecoveryFailed    JournalPhase = "recovery_failed"
)

const journalFileName = "upgrade-journal.json"

type JournalAsset struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Existed    bool   `json:"existed"`
	OldVersion int    `json:"old_version"`
	NewVersion int    `json:"new_version"`
	OldSHA256  string `json:"old_sha256,omitempty"`
	NewSHA256  string `json:"new_sha256,omitempty"`
	Migrated   bool   `json:"migrated"`
}

type Journal struct {
	UpgradeID         string         `json:"upgrade_id"`
	Phase             JournalPhase   `json:"phase"`
	OldProductVersion string         `json:"old_product_version"`
	NewProductVersion string         `json:"new_product_version"`
	SnapshotDir       string         `json:"snapshot_dir"`
	ManifestPath      string         `json:"manifest_path"`
	Assets            []JournalAsset `json:"assets"`
	CreatedAt         string         `json:"created_at"`
	UpdatedAt         string         `json:"updated_at"`
	Error             string         `json:"error,omitempty"`
}

type ExternalModificationError struct {
	AssetPath   string
	AssetName   string
	CurrentSHA  string
	OldSHA      string
	NewSHA      string
	SnapshotDir string
}

func (e *ExternalModificationError) Error() string {
	return fmt.Sprintf("upgrade: asset %q (%s) has unexpected external modification (sha256: %s, expected old: %s, expected new: %s); snapshot is preserved at %s, please resolve manually",
		e.AssetName, e.AssetPath, e.CurrentSHA, e.OldSHA, e.NewSHA, e.SnapshotDir)
}

type RecoveryError struct {
	Phase       JournalPhase
	SnapshotDir string
	Cause       error
}

func (e *RecoveryError) Error() string {
	return fmt.Sprintf("upgrade recovery failed (phase %s, snapshot %s): %v", e.Phase, e.SnapshotDir, e.Cause)
}

func (e *RecoveryError) Unwrap() error {
	return e.Cause
}

func journalPath(dataDir string) string {
	return filepath.Join(dataDir, journalFileName)
}

func readJournal(dataDir string) (*Journal, bool, error) {
	path := journalPath(dataDir)
	raw, err := readManagedFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("upgrade: read journal: %w", err)
	}
	var j Journal
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, false, fmt.Errorf("upgrade: invalid journal: %w", err)
	}
	return &j, true, nil
}

func writeJournal(dataDir string, j *Journal, now time.Time) error {
	j.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	raw, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("upgrade: marshal journal: %w", err)
	}
	return writeAtomic(journalPath(dataDir), raw, 0o600)
}

func randomToken(length int) string {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func validateJournalAgainstWhitelist(j *Journal, allowed []Asset, dataDir string) error {
	allowedPaths := map[string]bool{
		filepath.Clean(filepath.Join(dataDir, manifestFileName)): true,
	}
	for _, a := range allowed {
		allowedPaths[filepath.Clean(a.Path)] = true
	}
	for _, asset := range j.Assets {
		clean := filepath.Clean(asset.Path)
		if !allowedPaths[clean] {
			return fmt.Errorf("upgrade: journal contains asset %q with untrusted path %q", asset.Name, asset.Path)
		}
	}
	return nil
}

func checkExternalModifications(j *Journal) error {
	for _, asset := range j.Assets {
		if !asset.Migrated && asset.Existed {
			raw, err := os.ReadFile(asset.Path)
			if err != nil {
				return &ExternalModificationError{
					AssetPath:   asset.Path,
					AssetName:   asset.Name,
					CurrentSHA:  "<missing>",
					OldSHA:      asset.OldSHA256,
					NewSHA:      asset.NewSHA256,
					SnapshotDir: j.SnapshotDir,
				}
			}
			curSHA := digest(raw)
			if curSHA != asset.OldSHA256 {
				return &ExternalModificationError{
					AssetPath:   asset.Path,
					AssetName:   asset.Name,
					CurrentSHA:  curSHA,
					OldSHA:      asset.OldSHA256,
					NewSHA:      asset.NewSHA256,
					SnapshotDir: j.SnapshotDir,
				}
			}
			continue
		}

		if asset.Existed {
			raw, err := os.ReadFile(asset.Path)
			if err != nil {
				return &ExternalModificationError{
					AssetPath:   asset.Path,
					AssetName:   asset.Name,
					CurrentSHA:  "<missing>",
					OldSHA:      asset.OldSHA256,
					NewSHA:      asset.NewSHA256,
					SnapshotDir: j.SnapshotDir,
				}
			}
			curSHA := digest(raw)
			if curSHA != asset.OldSHA256 && curSHA != asset.NewSHA256 {
				return &ExternalModificationError{
					AssetPath:   asset.Path,
					AssetName:   asset.Name,
					CurrentSHA:  curSHA,
					OldSHA:      asset.OldSHA256,
					NewSHA:      asset.NewSHA256,
					SnapshotDir: j.SnapshotDir,
				}
			}
		} else {
			raw, err := os.ReadFile(asset.Path)
			if err == nil {
				curSHA := digest(raw)
				if curSHA != asset.NewSHA256 {
					return &ExternalModificationError{
						AssetPath:   asset.Path,
						AssetName:   asset.Name,
						CurrentSHA:  curSHA,
						OldSHA:      "<none>",
						NewSHA:      asset.NewSHA256,
						SnapshotDir: j.SnapshotDir,
					}
				}
			} else if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("upgrade: stat %s: %w", asset.Name, err)
			}
		}
	}
	return nil
}

func verifyAssetsMatchOld(j *Journal) error {
	for _, asset := range j.Assets {
		if asset.Existed {
			raw, err := os.ReadFile(asset.Path)
			if err != nil {
				return fmt.Errorf("asset %q missing: %w", asset.Name, err)
			}
			if curSHA := digest(raw); curSHA != asset.OldSHA256 {
				return fmt.Errorf("asset %q checksum mismatch: expected %s, got %s", asset.Name, asset.OldSHA256, curSHA)
			}
		} else {
			if _, err := os.Stat(asset.Path); !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("asset %q should not exist after rollback", asset.Name)
			}
		}
	}
	return nil
}

// recoverIncompleteUpgrade checks for an unfinished upgrade journal on disk before application stores open.
func recoverIncompleteUpgrade(opts Options) (*Result, error) {
	j, exists, err := readJournal(opts.DataDir)
	if err != nil {
		return nil, err
	}
	if !exists || j.Phase == PhaseComplete || j.Phase == PhaseIdle {
		return nil, nil
	}

	if j.Phase == PhaseRecoveryFailed {
		return nil, &RecoveryError{
			Phase:       PhaseRecoveryFailed,
			SnapshotDir: j.SnapshotDir,
			Cause:       errors.New(j.Error),
		}
	}

	if j.Phase == PhasePrepared {
		if err := verifyAssetsMatchOld(j); err == nil {
			j.Phase = PhaseComplete
			_ = writeJournal(opts.DataDir, j, opts.Now())
			return nil, nil
		}
	}

	if j.Phase == PhaseManifestCommitted {
		manifestPath := filepath.Join(opts.DataDir, manifestFileName)
		manifest, mExists, mErr := readManifest(manifestPath)
		if mErr == nil && mExists && manifest.ProductVersion == j.NewProductVersion {
			j.Phase = PhaseComplete
			_ = writeJournal(opts.DataDir, j, opts.Now())
			return &Result{
				Adopted:  false,
				Previous: manifest,
				Current:  manifest,
			}, nil
		}
	}

	if err := validateJournalAgainstWhitelist(j, opts.Assets, opts.DataDir); err != nil {
		return nil, err
	}

	if err := checkExternalModifications(j); err != nil {
		j.Phase = PhaseRecoveryFailed
		j.Error = err.Error()
		_ = writeJournal(opts.DataDir, j, opts.Now())
		return nil, err
	}

	snap, err := loadSnapshot(j.SnapshotDir)
	if err != nil {
		j.Phase = PhaseRecoveryFailed
		j.Error = fmt.Sprintf("load snapshot: %v", err)
		_ = writeJournal(opts.DataDir, j, opts.Now())
		return nil, &RecoveryError{Phase: PhaseRecoveryFailed, SnapshotDir: j.SnapshotDir, Cause: err}
	}
	if err := snap.verify(); err != nil {
		j.Phase = PhaseRecoveryFailed
		j.Error = fmt.Sprintf("verify snapshot: %v", err)
		_ = writeJournal(opts.DataDir, j, opts.Now())
		return nil, &RecoveryError{Phase: PhaseRecoveryFailed, SnapshotDir: j.SnapshotDir, Cause: err}
	}

	j.Phase = PhaseRecovering
	if err := writeJournal(opts.DataDir, j, opts.Now()); err != nil {
		return nil, err
	}

	if err := snap.restore(); err != nil {
		j.Phase = PhaseRecoveryFailed
		j.Error = fmt.Sprintf("restore: %v", err)
		_ = writeJournal(opts.DataDir, j, opts.Now())
		return nil, &RecoveryError{Phase: PhaseRecoveryFailed, SnapshotDir: j.SnapshotDir, Cause: err}
	}

	if err := verifyAssetsMatchOld(j); err != nil {
		j.Phase = PhaseRecoveryFailed
		j.Error = fmt.Sprintf("verify restored: %v", err)
		_ = writeJournal(opts.DataDir, j, opts.Now())
		return nil, &RecoveryError{Phase: PhaseRecoveryFailed, SnapshotDir: j.SnapshotDir, Cause: err}
	}

	j.Phase = PhaseRestored
	if err := writeJournal(opts.DataDir, j, opts.Now()); err != nil {
		return nil, err
	}

	j.Phase = PhaseComplete
	if err := writeJournal(opts.DataDir, j, opts.Now()); err != nil {
		return nil, err
	}

	return &Result{
		Recovered:         true,
		RecoveryBackupDir: j.SnapshotDir,
	}, nil
}

// Recover checks and recovers any incomplete upgrade transaction.
func Recover(opts Options) (*Result, error) {
	if strings.TrimSpace(opts.DataDir) == "" {
		return nil, errors.New("upgrade: data directory is empty")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := validateAssets(opts.Assets); err != nil {
		return nil, err
	}
	release, err := acquireLock(filepath.Join(opts.DataDir, lockFileName), opts.Now())
	if err != nil {
		return nil, err
	}
	defer release()

	return recoverIncompleteUpgrade(opts)
}
