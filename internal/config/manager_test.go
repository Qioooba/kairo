package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o640); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ResolvePaths(dir)
	return NewManager(cfg, path), path
}

func TestNewManager_GetPath(t *testing.T) {
	m, p := newTestManager(t)
	if m.Path() != p {
		t.Errorf("Path=%q, want %q", m.Path(), p)
	}
}

func TestManager_GetReturnsCurrent(t *testing.T) {
	m, _ := newTestManager(t)
	cfg := m.Get()
	if cfg == nil {
		t.Fatal("Get returned nil")
	}
	if cfg.App.Name != "TestBox" {
		t.Errorf("name: %q", cfg.App.Name)
	}
}

func TestManager_Replace_Happy(t *testing.T) {
	m, path := newTestManager(t)
	newCfg := m.Get()
	newCfg.Systems[0].Servers[0].Host = "10.0.0.99"
	if err := m.Replace(newCfg); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	// 内存中能看到新值
	if got := m.Get().Systems[0].Servers[0].Host; got != "10.0.0.99" {
		t.Errorf("in-memory host: %q", got)
	}
	// 磁盘上也被覆盖
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "10.0.0.99") {
		t.Errorf("disk file not updated: %s", b)
	}
}

func TestManager_Replace_RejectsInvalid(t *testing.T) {
	m, path := newTestManager(t)
	origBytes, _ := os.ReadFile(path)
	bad := &Config{
		App: AppConfig{Host: "0.0.0.0"}, // 不允许的 host
	}
	bad.Defaults()
	if err := m.Replace(bad); err == nil {
		t.Fatal("Replace should reject 0.0.0.0")
	}
	// 磁盘文件不应被破坏
	nowBytes, _ := os.ReadFile(path)
	if string(origBytes) != string(nowBytes) {
		t.Errorf("disk file should be unchanged after failed Replace")
	}
}

func TestManager_Replace_NilFails(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.Replace(nil); err == nil {
		t.Fatal("Replace(nil) should fail")
	}
}

func TestManager_Replace_Concurrent(t *testing.T) {
	// 10 个 goroutine 同时 Replace 不同值，验证不会出现脏读 / 死锁
	m, _ := newTestManager(t)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := m.Get()
			// 用 values 拷贝避免共享指针
			cp := *c
			cp.Systems[0].Servers[0].Host = "10.0.0." + string(rune('0'+i%10))
			_ = m.Replace(&cp)
		}(i)
	}
	wg.Wait()
	// 只要不死锁、不 panic，断言 Get 能拿到一份有效 Config
	if cfg := m.Get(); cfg == nil || len(cfg.Systems) == 0 {
		t.Fatal("Get returned invalid after concurrent Replace")
	}
}

func TestWriteYAMLAtomic_NoTempLeft(t *testing.T) {
	m, path := newTestManager(t)
	c := m.Get()
	cp := *c
	cp.Systems[0].Servers[0].Host = "10.0.0.55"
	if err := m.Replace(&cp); err != nil {
		t.Fatal(err)
	}
	// 检查没有 .tmp 残留
	dir := filepath.Dir(path)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".yaml.tmp") {
			t.Errorf("tmp file left: %s", e.Name())
		}
	}
}

func TestManager_TimeoutQuick(t *testing.T) {
	// Sanity: 验证 Replace 不会因为 yaml 写盘慢导致整体测试慢
	m, _ := newTestManager(t)
	c := m.Get()
	cp := *c
	cp.App.Name = "renamed"
	done := make(chan struct{})
	go func() {
		_ = m.Replace(&cp)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Replace took > 2s, should be near-instant")
	}
}
