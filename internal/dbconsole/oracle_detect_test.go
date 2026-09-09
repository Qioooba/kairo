package dbconsole

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPEArchitectureCurrentProcess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PE header inspection is Windows specific")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	bitness, compatible, err := InspectPEArchitecture(exe)
	if err != nil {
		t.Fatalf("InspectPEArchitecture(%s) failed: %v", exe, err)
	}
	t.Logf("Self executable architecture: %s (compatible=%v)", bitness, compatible)
	if !compatible {
		t.Fatalf("expected current executable to be compatible with runtime (%s)", runtime.GOARCH)
	}
}

func TestSelectBestCandidateRanking(t *testing.T) {
	curBitness := "64-bit"
	if runtime.GOARCH == "386" {
		curBitness = "32-bit"
	}
	otherBitness := "32-bit"
	if curBitness == "32-bit" {
		otherBitness = "64-bit"
	}

	candidates := []OracleCandidateClient{
		{
			Path:          `C:\incompatible_client`,
			Bitness:       otherBitness,
			Compatible:    false,
			Source:        "PL/SQL Developer",
			HasClientDLLs: true,
			HasTNS:        true,
		},
		{
			Path:          `C:\compatible_partial`,
			Bitness:       curBitness,
			Compatible:    true,
			Source:        "PATH",
			HasClientDLLs: false,
			HasTNS:        false,
		},
		{
			Path:          `C:\compatible_full`,
			Bitness:       curBitness,
			Compatible:    true,
			Source:        "Oracle Registry",
			HasClientDLLs: true,
			HasTNS:        true,
		},
	}

	best := SelectBestCandidate(candidates, "")
	if best == nil {
		t.Fatal("expected to select best candidate")
	}
	if best.Path != `C:\compatible_full` {
		t.Fatalf("expected C:\\compatible_full, got %s", best.Path)
	}

	// Custom override takes absolute precedence
	bestCustom := SelectBestCandidate(candidates, `C:\compatible_partial`)
	if bestCustom == nil || bestCustom.Path != `C:\compatible_partial` {
		t.Fatalf("expected custom override to be picked")
	}
}

func TestSystemOracleDetectionLive(t *testing.T) {
	cands, plsqlFound, plsqlBit, plsqlDetail := DetectSystemOracleClients(Source{})
	t.Logf("Detected %d Oracle Client candidates", len(cands))
	for i, c := range cands {
		t.Logf(" [%d] %s (%s, compatible=%v, has_dlls=%v, has_tns=%v) - %s",
			i, c.Path, c.Bitness, c.Compatible, c.HasClientDLLs, c.HasTNS, c.Source)
	}
	t.Logf("PL/SQL Developer: detected=%v, bitness=%s, detail=%s", plsqlFound, plsqlBit, plsqlDetail)
	best := SelectBestCandidate(cands, "")
	if best != nil {
		t.Logf("Best candidate: %s (%s) from %s", best.Path, best.Bitness, best.Source)
	} else {
		t.Logf("No compatible Oracle Client detected; pure-Go go-ora will handle Oracle safely")
	}
}

func TestFindTNSAdmin(t *testing.T) {
	tmp := t.TempDir()
	adminDir := filepath.Join(tmp, "network", "admin")
	if err := os.MkdirAll(adminDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "tnsnames.ora"), []byte("# test"), 0644); err != nil {
		t.Fatal(err)
	}
	found, ok := FindTNSAdmin(filepath.Join(tmp, "bin"))
	if !ok || filepath.Clean(found) != filepath.Clean(adminDir) {
		t.Fatalf("FindTNSAdmin failed: found=%s ok=%v", found, ok)
	}
}
