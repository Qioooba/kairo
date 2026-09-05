package waspack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageProvenanceBindsConfigurationAndAllowsWAREdits(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "工程 项目")
	output := filepath.Join(root, "输出 包")
	metadata := filepath.Join(root, "data")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	req := Request{
		ProjectDir: project, OutputDir: output, PackageName: "Demo", Manifest: "./a.class",
		AutoPair: true, PackType: "batch", BatchBaseDir: "/batch/credit", ChmodMode: "755", MetadataDir: metadata,
	}
	token, err := WriteStageProvenance(req, output)
	if err != nil || !strings.HasPrefix(token, "stage-") {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if err := os.WriteFile(filepath.Join(output, "war-edited.txt"), []byte("user edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	req.StageToken = token
	if err := ValidateStageProvenance(req, output); err != nil {
		t.Fatalf("edited WAR should remain valid: %v", err)
	}
	changed := req
	changed.Manifest = "./other.class"
	if err := ValidateStageProvenance(changed, output); err == nil || !strings.Contains(err.Error(), "重新抽取") {
		t.Fatalf("manifest mismatch err=%v", err)
	}
	changed = req
	changed.PackType = "app"
	if err := ValidateStageProvenance(changed, output); err == nil {
		t.Fatal("pack type mismatch must fail")
	}
	changed = req
	changed.StageToken = "stage-invalid"
	if err := ValidateStageProvenance(changed, output); err == nil {
		t.Fatal("token mismatch must fail")
	}
}

func TestStageProvenanceCorruptStoreFailsClosed(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	output := filepath.Join(root, "output")
	metadata := filepath.Join(root, "metadata")
	for _, dir := range []string{project, output, metadata} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	req := Request{ProjectDir: project, OutputDir: output, PackageName: "Demo", Manifest: "./a", MetadataDir: metadata}
	if err := os.WriteFile(filepath.Join(metadata, "waspack-provenance.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteStageProvenance(req, output); err == nil {
		t.Fatal("corrupt provenance must reject write")
	}
	if err := ValidateStageProvenance(req, output); err == nil {
		t.Fatal("missing/corrupt provenance must reject package")
	}
}
