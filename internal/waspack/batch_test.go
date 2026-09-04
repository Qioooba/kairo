package waspack

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchProjectPackaging(t *testing.T) {
	tmpDir := t.TempDir()
	projDir := filepath.Join(tmpDir, "batch_proj")
	outDir := filepath.Join(tmpDir, "output")

	// Create sample batch structure from user prompt
	files := []string{
		"amargci/credit_2nd.sh",
		"amargci/newcbsdata.sh",
		"amargci/etc/gci_task_2nd.xml",
		"AmarExtract/etc/ext_metadata.xml",
		"AmarExtract/etc/ext_credit_task_ql.xml",
		"AmarExtract/etc/ext_credit_are_ql.xml",
		"AmarExtract/src/jsyh/CustomerSpecialInfo.java",
		"AmarExtract/classes/jsyh/CustomerSpecialInfo.class",
		"AmarExtract/get_customerspecialinfo.sh",
		"AmarExtract/run_ql.sh",
		"AmarExtract/src/jsyh/UpdateExchangeRate.java",
		"AmarExtract/classes/jsyh/UpdateExchangeRate.class",
	}

	for _, rel := range files {
		abs := filepath.Join(projDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("echo test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	manifest := strings.Join(files, "\n")
	pkgName := "DDD20260714qijunV6"

	req := Request{
		ProjectDir:   projDir,
		OutputDir:    outDir,
		PackageName:  pkgName,
		Manifest:     manifest,
		AutoPair:     false,
		PackType:     "batch",
		BatchBaseDir: "/batch/credit",
	}

	res, err := Build(req)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if res.Files != len(files) {
		t.Fatalf("Expected %d files, got %d", len(files), res.Files)
	}

	// Verify chmod.txt exists and has expected lines
	chmodFile := filepath.Join(outDir, "chmod.txt")
	chmodData, err := os.ReadFile(chmodFile)
	if err != nil {
		t.Fatalf("Failed to read chmod.txt: %v", err)
	}
	chmodStr := string(chmodData)
	expectedChmod := []string{
		"chmod 777 /batch/credit/amargci/credit_2nd.sh",
		"chmod 777 /batch/credit/amargci/newcbsdata.sh",
		"chmod 777 /batch/credit/AmarExtract/get_customerspecialinfo.sh",
		"chmod 777 /batch/credit/AmarExtract/run_ql.sh",
	}
	for _, exp := range expectedChmod {
		if !strings.Contains(chmodStr, exp) {
			t.Errorf("chmod.txt missing %q, content:\n%s", exp, chmodStr)
		}
	}

	// Verify Bak script
	bakFile := filepath.Join(outDir, "Bak"+pkgName+".sh")
	bakData, err := os.ReadFile(bakFile)
	if err != nil {
		t.Fatalf("Failed to read Bak script: %v", err)
	}
	if !strings.HasPrefix(string(bakData), "tar -cvf Bak"+pkgName+".tar ./") {
		t.Errorf("Bak script format mismatch: %s", string(bakData))
	}

	// Verify Exec script
	execFile := filepath.Join(outDir, pkgName+".sh")
	execData, err := os.ReadFile(execFile)
	if err != nil {
		t.Fatalf("Failed to read exec script: %v", err)
	}
	if !strings.HasPrefix(string(execData), "tar -cvf "+pkgName+".tar ./") {
		t.Errorf("Exec script format mismatch: %s", string(execData))
	}

	// Verify tar entries do NOT have ./ prefix and start with manifest dirs like amargci/ or AmarExtract/
	tarFilePath := filepath.Join(outDir, pkgName+".tar")
	tf, err := os.Open(tarFilePath)
	if err != nil {
		t.Fatalf("Failed to open tar: %v", err)
	}
	defer tf.Close()
	tr := tar.NewReader(tf)
	tarCount := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		tarCount++
		if strings.HasPrefix(hdr.Name, "./") {
			t.Errorf("批量打包 tar 内路径不应包含 ./ 前缀，得到: %s", hdr.Name)
		}
		if !strings.HasPrefix(hdr.Name, "amargci/") && !strings.HasPrefix(hdr.Name, "AmarExtract/") {
			t.Errorf("批量打包压缩包第一层应为清单目录，得到: %s", hdr.Name)
		}
	}
	if tarCount != len(files) {
		t.Errorf("tar 条目数量期望 %d，实际 %d", len(files), tarCount)
	}

	// Verify .kairo-waspack.json is NOT created
	markerFile := filepath.Join(outDir, ".kairo-waspack.json")
	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Errorf(".kairo-waspack.json should NOT be created, but exists!")
	}
}
