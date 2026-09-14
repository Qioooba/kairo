package upgrade

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testDetectVersion(raw []byte) (int, error) {
	var val struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &val); err != nil {
		return 0, err
	}
	return val.Version, nil
}

// T056: Crash during Applying Phase -> Restart first recovers, no mixed versions as normal state.
func TestT056_CrashDuringApplyingAndRecovery(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}

	fileA := filepath.Join(dataDir, "asset_a.json")
	fileB := filepath.Join(dataDir, "asset_b.json")
	fileC := filepath.Join(dataDir, "asset_c.json")

	origA := []byte(`{"version":1,"name":"fileA"}`)
	origB := []byte(`{"version":1,"name":"fileB"}`)
	origC := []byte(`{"version":1,"name":"fileC"}`)

	_ = os.WriteFile(fileA, origA, 0o600)
	_ = os.WriteFile(fileB, origB, 0o600)
	_ = os.WriteFile(fileC, origC, 0o600)

	assetsV1 := []Asset{
		{Name: "asset_a", Path: fileA, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion},
		{Name: "asset_b", Path: fileB, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion},
		{Name: "asset_c", Path: fileC, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion},
	}

	// Initial adoption
	initRes, err := Run(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.17",
		Now:            fixedNow,
		Assets:         assetsV1,
	})
	if err != nil {
		t.Fatalf("initial run failed: %v", err)
	}
	if !initRes.Adopted {
		t.Fatal("expected initial adoption")
	}

	// Now configure upgrade v1 -> v2 for all 3 assets
	v2Migration := func(name string) Migration {
		return func(raw []byte) ([]byte, error) {
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, err
			}
			m["version"] = 2
			m["upgraded"] = true
			return json.Marshal(m)
		}
	}

	assetsV2 := []Asset{
		{
			Name: "asset_a", Path: fileA, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
			Migrations: map[int]Migration{1: v2Migration("asset_a")},
		},
		{
			Name: "asset_b", Path: fileB, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
			Migrations: map[int]Migration{1: v2Migration("asset_b")},
		},
		{
			Name: "asset_c", Path: fileC, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
			Migrations: map[int]Migration{1: v2Migration("asset_c")},
		},
	}

	// Crash injection: kill/fail right after asset_a is committed!
	crashErr := errors.New("simulated crash after asset_a commit")
	_, err = Run(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.18",
		Now:            fixedNow,
		Assets:         assetsV2,
		OnAssetCommitted: func(assetName string) error {
			if assetName == "asset_a" {
				return crashErr
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "simulated crash") {
		t.Fatalf("expected simulated crash, got: %v", err)
	}

	// Notice that rollback occurred during standard error handling, restoring files.
	// Now let's simulate a HARD CRASH (kill -9) where in-memory rollback does NOT run:
	// We manually tamper disk into the exact "mid-applying" state:
	// Journal is in PhaseApplying, asset_a is v2, asset_b is v1, asset_c is v1.
	j, jExists, err := readJournal(dataDir)
	if err != nil || !jExists {
		t.Fatalf("expected journal to exist: exists=%v err=%v", jExists, err)
	}
	// Restore snapshot path from journal
	snapshotDir := j.SnapshotDir

	// Simulate hard crash after writing asset_a:
	v2A, _ := v2Migration("asset_a")(origA)
	_ = os.WriteFile(fileA, v2A, 0o600)
	_ = os.WriteFile(fileB, origB, 0o600)
	_ = os.WriteFile(fileC, origC, 0o600)
	j.Phase = PhaseApplying
	j.Error = ""
	if err := writeJournal(dataDir, j, fixedNow()); err != nil {
		t.Fatal(err)
	}

	// Verify disk is in a torn / mixed state
	readA, _ := os.ReadFile(fileA)
	if !strings.Contains(string(readA), `"version":2`) {
		t.Fatal("expected fileA to be v2 in torn state")
	}
	readB, _ := os.ReadFile(fileB)
	if !strings.Contains(string(readB), `"version":1`) {
		t.Fatal("expected fileB to be v1 in torn state")
	}

	// Sub-case 1: Test Recover() directly
	recRes, err := Recover(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.18",
		Now:            fixedNow,
		Assets:         assetsV2,
	})
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if !recRes.Recovered || recRes.RecoveryBackupDir != snapshotDir {
		t.Fatalf("unexpected recovery result: %+v", recRes)
	}

	// Check that all files are back to v1! No mixed versions.
	afterRecA, _ := os.ReadFile(fileA)
	afterRecB, _ := os.ReadFile(fileB)
	afterRecC, _ := os.ReadFile(fileC)
	if string(afterRecA) != string(origA) || string(afterRecB) != string(origB) || string(afterRecC) != string(origC) {
		t.Fatalf("files not restored: A=%s B=%s C=%s", afterRecA, afterRecB, afterRecC)
	}

	// Sub-case 2: Test that a subsequent Run(opts) cleanly upgrades from the recovered state
	finalRes, err := Run(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.18",
		Now:            fixedNow,
		Assets:         assetsV2,
	})
	if err != nil {
		t.Fatalf("subsequent Run failed: %v", err)
	}
	if len(finalRes.Migrated) != 3 {
		t.Fatalf("expected 3 files migrated, got: %v", finalRes.Migrated)
	}
	finalA, _ := os.ReadFile(fileA)
	finalB, _ := os.ReadFile(fileB)
	finalC, _ := os.ReadFile(fileC)
	if !strings.Contains(string(finalA), `"version":2`) ||
		!strings.Contains(string(finalB), `"version":2`) ||
		!strings.Contains(string(finalC), `"version":2`) {
		t.Fatalf("files not cleanly upgraded to v2 after recovery")
	}
}

// T057: External modification between crash and restart -> refuse overwrite, report manual resolution path.
func TestT057_ExternalModificationRefusesDestructiveOverwrite(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}

	fileA := filepath.Join(dataDir, "asset_a.json")
	fileB := filepath.Join(dataDir, "asset_b.json")

	origA := []byte(`{"version":1,"name":"fileA"}`)
	origB := []byte(`{"version":1,"name":"fileB"}`)

	_ = os.WriteFile(fileA, origA, 0o600)
	_ = os.WriteFile(fileB, origB, 0o600)

	assetsV1 := []Asset{
		{Name: "asset_a", Path: fileA, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion},
		{Name: "asset_b", Path: fileB, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion},
	}

	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: assetsV1}); err != nil {
		t.Fatal(err)
	}

	assetsV2 := []Asset{
		{
			Name: "asset_a", Path: fileA, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
			Migrations: map[int]Migration{1: func(raw []byte) ([]byte, error) { return []byte(`{"version":2,"name":"fileA"}`), nil }},
		},
		{
			Name: "asset_b", Path: fileB, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
			Migrations: map[int]Migration{1: func(raw []byte) ([]byte, error) { return []byte(`{"version":2,"name":"fileB"}`), nil }},
		},
	}

	// Create a snapshot and simulate journal in PhaseApplying
	prepared := []preparedAsset{
		{asset: assetsV2[0], raw: []byte(`{"version":2,"name":"fileA"}`), oldRaw: origA, fromVersion: 1, state: AssetState{Version: 2, SHA256: digest([]byte(`{"version":2,"name":"fileA"}`))}},
		{asset: assetsV2[1], raw: []byte(`{"version":2,"name":"fileB"}`), oldRaw: origB, fromVersion: 1, state: AssetState{Version: 2, SHA256: digest([]byte(`{"version":2,"name":"fileB"}`))}},
	}
	snap, err := createSnapshot(dataDir, prepared, fixedNow())
	if err != nil {
		t.Fatal(err)
	}

	j := &Journal{
		UpgradeID:         "upg-test-crash",
		Phase:             PhaseApplying,
		OldProductVersion: "v0.17",
		NewProductVersion: "v0.18",
		SnapshotDir:       snap.dir,
		ManifestPath:      filepath.Join(dataDir, manifestFileName),
		Assets: []JournalAsset{
			{
				Name: "asset_a", Path: fileA, Existed: true, OldVersion: 1, NewVersion: 2,
				OldSHA256: digest(origA), NewSHA256: digest([]byte(`{"version":2,"name":"fileA"}`)), Migrated: true,
			},
			{
				Name: "asset_b", Path: fileB, Existed: true, OldVersion: 1, NewVersion: 2,
				OldSHA256: digest(origB), NewSHA256: digest([]byte(`{"version":2,"name":"fileB"}`)), Migrated: true,
			},
			{
				Name: "upgrade-manifest", Path: filepath.Join(dataDir, manifestFileName), Existed: true, OldVersion: 1, NewVersion: 1,
				OldSHA256: digest([]byte(`{}`)), NewSHA256: digest([]byte(`{"new":true}`)), Migrated: true,
			},
		},
	}
	if err := writeJournal(dataDir, j, fixedNow()); err != nil {
		t.Fatal(err)
	}

	// FileA was partially written to v2
	_ = os.WriteFile(fileA, []byte(`{"version":2,"name":"fileA"}`), 0o600)

	// BUT FileB was externally modified while stopped!
	externalContent := []byte(`{"externally_modified":true,"user_data":"unsaved_work"}`)
	_ = os.WriteFile(fileB, externalContent, 0o600)

	// Now attempt Run or Recover
	res, err := Run(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.18",
		Now:            fixedNow,
		Assets:         assetsV2,
	})
	if err == nil {
		t.Fatalf("expected external modification error, got success: %+v", res)
	}

	var extErr *ExternalModificationError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected ExternalModificationError, got: %v", err)
	}
	if extErr.AssetName != "asset_b" {
		t.Fatalf("expected asset_b in error, got %s", extErr.AssetName)
	}
	if extErr.SnapshotDir != snap.dir {
		t.Fatalf("expected snapshot dir %s in error, got %s", snap.dir, extErr.SnapshotDir)
	}
	if !strings.Contains(err.Error(), "manual") {
		t.Fatalf("expected manual resolution guidance in error, got: %s", err.Error())
	}

	// CRITICAL CHECK: User's external modification MUST NOT be destroyed!
	afterFileB, _ := os.ReadFile(fileB)
	if string(afterFileB) != string(externalContent) {
		t.Fatalf("external file was overwritten! content=%s", afterFileB)
	}

	// Check that journal phase is PhaseRecoveryFailed
	jAfter, _, _ := readJournal(dataDir)
	if jAfter.Phase != PhaseRecoveryFailed {
		t.Fatalf("expected journal phase recovery_failed, got: %s", jAfter.Phase)
	}
}

// T058: Manifest committed before crash -> restart finishes cleanup; repeated restart is idempotent; old binary refuses.
func TestT058_ManifestCommittedCleanupAndIdempotence(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}

	fileA := filepath.Join(dataDir, "asset_a.json")
	origA := []byte(`{"version":1,"name":"fileA"}`)
	_ = os.WriteFile(fileA, origA, 0o600)

	assetsV1 := []Asset{
		{Name: "asset_a", Path: fileA, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion},
	}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: assetsV1}); err != nil {
		t.Fatal(err)
	}

	assetsV2 := []Asset{
		{
			Name: "asset_a", Path: fileA, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
			Migrations: map[int]Migration{1: func(raw []byte) ([]byte, error) { return []byte(`{"version":2,"name":"fileA"}`), nil }},
		},
	}

	// Inject crash during cleanup: right after PhaseManifestCommitted is written!
	crashManifestCommitted := errors.New("simulated crash during cleanup after manifest committed")
	_, err := Run(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.18",
		Now:            fixedNow,
		Assets:         assetsV2,
		OnPhaseTransition: func(phase JournalPhase, j *Journal) error {
			if phase == PhaseManifestCommitted {
				return crashManifestCommitted
			}
			return nil
		},
	})
	if err == nil || !errors.Is(err, crashManifestCommitted) {
		t.Fatalf("expected crash at manifest_committed, got %v", err)
	}

	// Check journal on disk: Phase is PhaseManifestCommitted
	j, _, err := readJournal(dataDir)
	if err != nil || j.Phase != PhaseManifestCommitted {
		t.Fatalf("expected PhaseManifestCommitted in journal, got: %v", j)
	}

	// First restart: should detect manifest was already committed, complete cleanup, not rollback
	res1, err := Run(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.18",
		Now:            fixedNow,
		Assets:         assetsV2,
	})
	if err != nil {
		t.Fatalf("restart after manifest_committed failed: %v", err)
	}
	if res1.Recovered {
		t.Fatal("manifest was already committed; should not rollback")
	}

	// Journal should now be PhaseComplete
	j2, _, _ := readJournal(dataDir)
	if j2.Phase != PhaseComplete {
		t.Fatalf("expected PhaseComplete, got: %s", j2.Phase)
	}

	// Second restart (idempotency check): should be clean no-op
	res2, err := Run(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.18",
		Now:            fixedNow,
		Assets:         assetsV2,
	})
	if err != nil {
		t.Fatalf("second restart failed: %v", err)
	}
	if len(res2.Migrated) != 0 {
		t.Fatalf("expected 0 migrations on second start, got: %v", res2.Migrated)
	}

	// Third restart with OLD binary (v0.17): must refuse downgrade!
	_, err = Run(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.17",
		Now:            fixedNow,
		Assets:         assetsV1,
	})
	if err == nil || !strings.Contains(err.Error(), "refusing product downgrade") {
		t.Fatalf("expected old binary to refuse downgrade, got: %v", err)
	}
}

// TestCorruptedSnapshotRefusesRestore: if snapshot files were corrupted, do not overwrite good files.
func TestCorruptedSnapshotRefusesRestore(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	_ = os.MkdirAll(dataDir, 0o700)

	fileA := filepath.Join(dataDir, "asset_a.json")
	origA := []byte(`{"version":1,"name":"fileA"}`)
	_ = os.WriteFile(fileA, origA, 0o600)

	assetsV1 := []Asset{
		{Name: "asset_a", Path: fileA, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion},
	}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: assetsV1}); err != nil {
		t.Fatal(err)
	}

	assetsV2 := []Asset{
		{
			Name: "asset_a", Path: fileA, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
			Migrations: map[int]Migration{1: func(raw []byte) ([]byte, error) { return []byte(`{"version":2,"name":"fileA"}`), nil }},
		},
	}

	prepared := []preparedAsset{
		{asset: assetsV2[0], raw: []byte(`{"version":2,"name":"fileA"}`), oldRaw: origA, fromVersion: 1, state: AssetState{Version: 2, SHA256: digest([]byte(`{"version":2,"name":"fileA"}`))}},
	}
	snap, err := createSnapshot(dataDir, prepared, fixedNow())
	if err != nil {
		t.Fatal(err)
	}

	// Intentionally tamper the backup file in snapshot directory!
	backupFile := filepath.Join(snap.dir, "000-asset_a.json")
	_ = os.WriteFile(backupFile, []byte(`{"corrupted":true}`), 0o600)

	j := &Journal{
		UpgradeID:         "upg-corrupt-snap",
		Phase:             PhaseApplying,
		OldProductVersion: "v0.17",
		NewProductVersion: "v0.18",
		SnapshotDir:       snap.dir,
		ManifestPath:      filepath.Join(dataDir, manifestFileName),
		Assets: []JournalAsset{
			{
				Name: "asset_a", Path: fileA, Existed: true, OldVersion: 1, NewVersion: 2,
				OldSHA256: digest(origA), NewSHA256: digest([]byte(`{"version":2,"name":"fileA"}`)), Migrated: true,
			},
		},
	}
	if err := writeJournal(dataDir, j, fixedNow()); err != nil {
		t.Fatal(err)
	}

	// Run recovery
	_, err = Recover(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.18",
		Now:            fixedNow,
		Assets:         assetsV2,
	})
	if err == nil || !strings.Contains(err.Error(), "corrupted backup") {
		t.Fatalf("expected corrupted backup error, got: %v", err)
	}
}

// TestCrashAtPhasePrepared: crash before any file is touched.
func TestCrashAtPhasePrepared(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	_ = os.MkdirAll(dataDir, 0o700)

	fileA := filepath.Join(dataDir, "asset_a.json")
	origA := []byte(`{"version":1,"name":"fileA"}`)
	_ = os.WriteFile(fileA, origA, 0o600)

	assetsV1 := []Asset{
		{Name: "asset_a", Path: fileA, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion},
	}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: assetsV1}); err != nil {
		t.Fatal(err)
	}

	assetsV2 := []Asset{
		{
			Name: "asset_a", Path: fileA, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
			Migrations: map[int]Migration{1: func(raw []byte) ([]byte, error) { return []byte(`{"version":2,"name":"fileA"}`), nil }},
		},
	}

	// Crash at PhasePrepared
	crashPrepared := errors.New("simulated crash at prepared")
	_, err := Run(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.18",
		Now:            fixedNow,
		Assets:         assetsV2,
		OnPhaseTransition: func(phase JournalPhase, j *Journal) error {
			if phase == PhasePrepared {
				return crashPrepared
			}
			return nil
		},
	})
	if err == nil || !errors.Is(err, crashPrepared) {
		t.Fatalf("expected crash at prepared, got: %v", err)
	}

	// Check journal is PhasePrepared
	j, _, _ := readJournal(dataDir)
	if j.Phase != PhasePrepared {
		t.Fatalf("expected PhasePrepared, got %s", j.Phase)
	}

	// Restart: recovers cleanly and completes migration
	res, err := Run(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.18",
		Now:            fixedNow,
		Assets:         assetsV2,
	})
	if err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if len(res.Migrated) != 1 {
		t.Fatalf("expected 1 migration, got: %v", res.Migrated)
	}
	content, _ := os.ReadFile(fileA)
	if !strings.Contains(string(content), `"version":2`) {
		t.Fatal("fileA not upgraded to v2")
	}
}

// Subprocess crash injection test using real os.Process.Kill()
func TestSubprocessCrashAndRecovery(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_CRASH") == "1" {
		dataDir := os.Getenv("TEST_DATA_DIR")
		fileA := filepath.Join(dataDir, "asset_a.json")
		fileB := filepath.Join(dataDir, "asset_b.json")

		assetsV2 := []Asset{
			{
				Name: "asset_a", Path: fileA, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
				Migrations: map[int]Migration{1: func(raw []byte) ([]byte, error) { return []byte(`{"version":2,"name":"fileA"}`), nil }},
			},
			{
				Name: "asset_b", Path: fileB, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
				Migrations: map[int]Migration{1: func(raw []byte) ([]byte, error) { return []byte(`{"version":2,"name":"fileB"}`), nil }},
			},
		}

		// In subprocess: kill self immediately after asset_a is committed!
		_, _ = Run(Options{
			DataDir:        dataDir,
			ProductVersion: "v0.18",
			Assets:         assetsV2,
			OnAssetCommitted: func(assetName string) error {
				if assetName == "asset_a" {
					// Hard kill without in-memory cleanup
					p, _ := os.FindProcess(os.Getpid())
					_ = p.Kill()
					os.Exit(1)
				}
				return nil
			},
		})
		os.Exit(0)
		return
	}

	// Parent test runner:
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	_ = os.MkdirAll(dataDir, 0o700)

	fileA := filepath.Join(dataDir, "asset_a.json")
	fileB := filepath.Join(dataDir, "asset_b.json")

	origA := []byte(`{"version":1,"name":"fileA"}`)
	origB := []byte(`{"version":1,"name":"fileB"}`)
	_ = os.WriteFile(fileA, origA, 0o600)
	_ = os.WriteFile(fileB, origB, 0o600)

	assetsV1 := []Asset{
		{Name: "asset_a", Path: fileA, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion},
		{Name: "asset_b", Path: fileB, CurrentVersion: 1, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion},
	}
	if _, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.17", Now: fixedNow, Assets: assetsV1}); err != nil {
		t.Fatal(err)
	}

	// Spawn subprocess with crash flag
	cmd := exec.Command(os.Args[0], "-test.run=TestSubprocessCrashAndRecovery")
	cmd.Env = append(os.Environ(), "TEST_SUBPROCESS_CRASH=1", "TEST_DATA_DIR="+dataDir)
	_ = cmd.Run() // Expect non-zero / killed

	// Check on disk: fileA was committed with v2, but fileB is still v1!
	curA, _ := os.ReadFile(fileA)
	curB, _ := os.ReadFile(fileB)
	if !strings.Contains(string(curA), `"version":2`) || !strings.Contains(string(curB), `"version":1`) {
		t.Fatalf("expected torn disk state: fileA=%s fileB=%s", curA, curB)
	}

	// Remove the lock held by the abruptly killed process before restarting
	_ = os.Remove(filepath.Join(dataDir, lockFileName))

	// Now run parent process Run: should recover to v1 first, then complete upgrade to v2!
	assetsV2 := []Asset{
		{
			Name: "asset_a", Path: fileA, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
			Migrations: map[int]Migration{1: func(raw []byte) ([]byte, error) { return []byte(`{"version":2,"name":"fileA"}`), nil }},
		},
		{
			Name: "asset_b", Path: fileB, CurrentVersion: 2, Critical: true, Validate: ValidateJSON, DetectVersion: testDetectVersion,
			Migrations: map[int]Migration{1: func(raw []byte) ([]byte, error) { return []byte(`{"version":2,"name":"fileB"}`), nil }},
		},
	}

	recRes, err := Run(Options{
		DataDir:        dataDir,
		ProductVersion: "v0.18",
		Now:            fixedNow,
		Assets:         assetsV2,
	})
	if err != nil {
		t.Fatalf("parent Run failed after subprocess crash: %v", err)
	}
	if !recRes.Recovered {
		t.Fatal("expected recovery to take place")
	}
	recA, _ := os.ReadFile(fileA)
	recB, _ := os.ReadFile(fileB)
	if !strings.Contains(string(recA), `"version":2`) || !strings.Contains(string(recB), `"version":2`) {
		t.Fatalf("expected both files to be cleanly upgraded to v2: fileA=%s fileB=%s", recA, recB)
	}
}
