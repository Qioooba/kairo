package browserpref

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestInitAndPath(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp)
	p, err := Path()
	if err != nil {
		t.Fatalf("Path() err = %v", err)
	}
	want := filepath.Join(tmp, fileName)
	if p != want {
		t.Fatalf("Path() = %q, want %q", p, want)
	}
}

func TestPathWithoutInit(t *testing.T) {
	// 清掉全局 dataDir
	dataDirMu.Lock()
	dataDir = ""
	dataDirMu.Unlock()

	if _, err := Path(); err == nil {
		t.Fatal("Path() should error when Init never called")
	}
}

func TestReadMissing(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp)

	s, err := Read()
	if err != nil {
		t.Fatalf("Read() err = %v, want nil (first run)", err)
	}
	if s != nil {
		t.Fatalf("Read() = %+v, want nil", s)
	}
}

func TestWriteReadRoundtrip(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp)

	now := time.Now().Truncate(time.Second)
	in := &State{
		Kind:      KindChrome,
		Path:      `C:\Program Files\Google\Chrome\Application\chrome.exe`,
		UpdatedAt: now,
	}
	if err := Write(in); err != nil {
		t.Fatalf("Write() err = %v", err)
	}

	out, err := Read()
	if err != nil {
		t.Fatalf("Read() err = %v", err)
	}
	if out == nil {
		t.Fatal("Read() = nil, want state")
	}
	if out.Kind != in.Kind {
		t.Errorf("Kind = %q, want %q", out.Kind, in.Kind)
	}
	if out.Path != in.Path {
		t.Errorf("Path = %q, want %q", out.Path, in.Path)
	}
	if !out.UpdatedAt.Equal(in.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", out.UpdatedAt, in.UpdatedAt)
	}
}

func TestWriteDefaultKindNoPath(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp)

	in := &State{Kind: KindDefault}
	if err := Write(in); err != nil {
		t.Fatalf("Write() err = %v", err)
	}

	out, err := Read()
	if err != nil {
		t.Fatalf("Read() err = %v", err)
	}
	if out.Kind != KindDefault {
		t.Errorf("Kind = %q, want %q", out.Kind, KindDefault)
	}
	if out.Path != "" {
		t.Errorf("Path = %q, want empty", out.Path)
	}
}

func TestWriteSetsUpdatedAtIfZero(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp)

	before := time.Now()
	in := &State{Kind: KindChrome, Path: "/some/chrome"}
	if err := Write(in); err != nil {
		t.Fatalf("Write() err = %v", err)
	}
	after := time.Now()

	out, err := Read()
	if err != nil {
		t.Fatalf("Read() err = %v", err)
	}
	if out.UpdatedAt.Before(before) || out.UpdatedAt.After(after.Add(time.Second)) {
		t.Errorf("UpdatedAt = %v, want in [%v, %v]", out.UpdatedAt, before, after)
	}
}

func TestWriteNil(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp)
	if err := Write(nil); err == nil {
		t.Fatal("Write(nil) should error")
	}
}

func TestReadCorruptJSON(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp)
	path, _ := Path()
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Read(); err == nil {
		t.Fatal("Read() should error on corrupt JSON")
	}
}

func TestReadEmptyFile(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp)
	path, _ := Path()
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Read()
	if err != nil {
		t.Fatalf("Read() err = %v, want nil on empty file", err)
	}
	if s != nil {
		t.Fatalf("Read() = %+v, want nil", s)
	}
}

func TestReadUnknownKindFallback(t *testing.T) {
	// state 里出现未知的 kind（未来的版本加新枚举值），读出来时
	// 不该让调用方错愕 —— 兜底回 default，至少"打开"还能跑。
	tmp := t.TempDir()
	Init(tmp)
	path, _ := Path()
	raw, _ := json.Marshal(map[string]any{
		"kind":       "firefox",
		"path":       "",
		"updated_at": time.Now(),
	})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Read()
	if err != nil {
		t.Fatalf("Read() err = %v", err)
	}
	if s.Kind != KindDefault {
		t.Errorf("Kind = %q, want %q (fallback)", s.Kind, KindDefault)
	}
}

func TestReset(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp)

	if err := Write(&State{Kind: KindChrome, Path: "/some/chrome"}); err != nil {
		t.Fatal(err)
	}
	if err := Reset(); err != nil {
		t.Fatalf("Reset() err = %v", err)
	}
	// Reset 之后读 → nil
	s, err := Read()
	if err != nil {
		t.Fatalf("Read() err = %v", err)
	}
	if s != nil {
		t.Fatalf("Read() = %+v, want nil after reset", s)
	}

	// Reset 不存在的文件也要成功（幂等）
	if err := Reset(); err != nil {
		t.Fatalf("Reset() on missing = %v, want nil", err)
	}
}

func TestWriteOverwrite(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp)

	if err := Write(&State{Kind: KindChrome, Path: "/old/chrome"}); err != nil {
		t.Fatal(err)
	}
	if err := Write(&State{Kind: KindDefault}); err != nil {
		t.Fatal(err)
	}
	s, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != KindDefault {
		t.Errorf("Kind = %q, want %q", s.Kind, KindDefault)
	}
	if s.Path != "" {
		t.Errorf("Path = %q, want empty after overwrite", s.Path)
	}
}

// TestWriteTempFileCleaned 验证 Write 成功后 .tmp 不残留。
// （defer os.Remove 在 Rename 后会返回 ErrNotExist，但不应该 panic；
// 真正担心的是 Write 失败路径下 .tmp 残留 —— 我们看一下 Write 失败后
// 状态是否正确。但要构造失败比较复杂，本测试只校验成功路径。）
func TestWriteTempFileCleaned(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows 上 .tmp rename 后仍可能被文件系统锁定，等到我们的 defer
		// os.Remove 跑时可能短暂失败，但最终会被系统回收。
		t.Skip("windows: tmp file may be locked briefly after rename")
	}
	tmp := t.TempDir()
	Init(tmp)
	if err := Write(&State{Kind: KindChrome, Path: "/some/chrome"}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file %q should be cleaned up", e.Name())
		}
	}
}
