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

// ---------- v0.5 P0 #6 + #20 回归测试 ----------
//
// #6: GBK 编码保存后还能读回 gbk（不静默改 utf-8）
// #20: 配置修改后落盘，重启后还能读回（PUT → kill → reload → GET 全链路）

// TestReplace_GBKEncoding_Preserved 验证 PUT 把 utf-8 改成 gbk 后，
// 内存 + 磁盘 + 重新 Load 三处都还是 gbk。
//
// 这是 #6 的核心契约：用户在前端把日志目录改成 gbk → 保存 → 刷新页面 → 看到 gbk。
// 之前担心 "Defaults() 把 gbk 改回 utf-8" / "Validate 把 gbk 拒掉" /
// "yaml 序列化丢字段"，这里一次性覆盖。
func TestReplace_GBKEncoding_Preserved(t *testing.T) {
	m, path := newTestManager(t)

	// 1) 通过 Replace 把 encoding 从 utf-8 改成 gbk
	c := m.Get()
	cp := *c
	cp.Systems[0].Servers[0].LogDirs[0].Encoding = "gbk"
	if err := m.Replace(&cp); err != nil {
		t.Fatalf("Replace 拒绝 gbk: %v", err)
	}

	// 2) 内存中能看到 gbk
	if got := m.Get().Systems[0].Servers[0].LogDirs[0].Encoding; got != "gbk" {
		t.Errorf("in-memory encoding: %q, want gbk", got)
	}

	// 3) 磁盘 yaml 里也是 gbk
	diskBytes, _ := os.ReadFile(path)
	if !strings.Contains(string(diskBytes), "encoding: gbk") {
		t.Errorf("磁盘 yaml 不含 'encoding: gbk':\n%s", diskBytes)
	}

	// 4) 模拟"重启"：从磁盘重新 Load 一份新 Manager
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("重启后 Load 失败: %v", err)
	}
	if got := reloaded.Systems[0].Servers[0].LogDirs[0].Encoding; got != "gbk" {
		t.Errorf("重启后 Load 出来的 encoding: %q, want gbk", got)
	}

	// 5) 大小写归一：前端可能发 'GBK' / 'gb18030'，Defaults 应归一到 'gbk'
	m2, _ := newTestManager(t)
	c2 := m2.Get()
	cp2 := *c2
	cp2.Systems[0].Servers[0].LogDirs[0].Encoding = "GBK"
	if err := m2.Replace(&cp2); err != nil {
		t.Fatalf("Replace 拒绝 GBK: %v", err)
	}
	if got := m2.Get().Systems[0].Servers[0].LogDirs[0].Encoding; got != "gbk" {
		t.Errorf("GBK 应归一为 gbk，得到: %q", got)
	}
}

// TestReplace_UTF8Encoding_StillWorks 验证改回 utf-8 也能 round-trip（不退化）。
func TestReplace_UTF8Encoding_StillWorks(t *testing.T) {
	m, _ := newTestManager(t)
	c := m.Get()
	cp := *c
	// 初始就是 utf-8，先改成 gbk 再改回 utf-8
	cp.Systems[0].Servers[0].LogDirs[0].Encoding = "gbk"
	if err := m.Replace(&cp); err != nil {
		t.Fatal(err)
	}
	cp2 := m.Get()
	cp2.Systems[0].Servers[0].LogDirs[0].Encoding = "utf-8"
	if err := m.Replace(cp2); err != nil {
		t.Fatalf("改回 utf-8 失败: %v", err)
	}
	if got := m.Get().Systems[0].Servers[0].LogDirs[0].Encoding; got != "utf-8" {
		t.Errorf("encoding: %q, want utf-8", got)
	}
}

// TestReplace_RejectsBadEncoding 验证非法 encoding 被拒，磁盘文件不被破坏。
//
// 这是 #6 修复的"副作用安全网"：用户可能手敲 'shift-jis' 配错，
// 我们要明确报错而不是静默改 utf-8。
//
// 必须用 Clone() 拿一份独立 Config —— 浅拷贝会共享 inner slice，
// 改 cp.Systems[0]...Encoding 等于改 m.Get() 的 inner data，
// 测不到"Replace 拒掉后内存未变"这条断言（mock 了 test 自身的 invariant）。
func TestReplace_RejectsBadEncoding(t *testing.T) {
	m, path := newTestManager(t)
	origBytes, _ := os.ReadFile(path)

	cp := m.Get().Clone()
	cp.Systems[0].Servers[0].LogDirs[0].Encoding = "shift-jis"
	err := m.Replace(cp)
	if err == nil {
		t.Fatal("Replace 应拒绝非法 encoding shift-jis")
	}
	if !strings.Contains(err.Error(), "encoding") {
		t.Errorf("错误应提到 encoding，得到: %v", err)
	}

	// 磁盘文件必须没动
	nowBytes, _ := os.ReadFile(path)
	if string(origBytes) != string(nowBytes) {
		t.Error("非法 encoding 应被拒，磁盘文件应保持不变")
	}

	// 内存也保持原值
	if got := m.Get().Systems[0].Servers[0].LogDirs[0].Encoding; got != "utf-8" {
		t.Errorf("内存中被改成: %q, 期望保持 utf-8", got)
	}
}

// TestReplace_FullRestartRoundTrip 模拟完整生命周期：
//
//	Load(初始 yaml) → Manager → 改 encoding → Replace → 重新 Load（模拟重启） → Manager → 改 host → Replace → 重新 Load
//
// 验证全程任何时刻 Get 都能拿到一致的数据，且磁盘文件始终是合法的 yaml。
//
// 这是 #20 的端到端单测版（不依赖 HTTP）。
func TestReplace_FullRestartRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o640); err != nil {
		t.Fatal(err)
	}

	// 阶段 1: 启动
	cfg1, err := Load(path)
	if err != nil {
		t.Fatalf("阶段 1 Load: %v", err)
	}
	m1 := NewManager(cfg1, path)
	if got := m1.Get().App.Name; got != "TestBox" {
		t.Fatalf("阶段 1 name: %q", got)
	}

	// 阶段 2: 改 encoding 为 gbk 并 PUT
	c1 := m1.Get()
	cp1 := *c1
	cp1.Systems[0].Servers[0].LogDirs[0].Encoding = "gbk"
	if err := m1.Replace(&cp1); err != nil {
		t.Fatalf("阶段 2 Replace: %v", err)
	}

	// 阶段 3: 模拟 kill 进程 → 重启 → 重新 Load
	cfg2, err := Load(path)
	if err != nil {
		t.Fatalf("阶段 3 Load 失败: %v", err)
	}
	m2 := NewManager(cfg2, path)
	if got := m2.Get().Systems[0].Servers[0].LogDirs[0].Encoding; got != "gbk" {
		t.Fatalf("阶段 3 重启后 encoding: %q, want gbk", got)
	}

	// 阶段 4: 再改 host 并 PUT
	c2 := m2.Get()
	cp2 := *c2
	cp2.Systems[0].Servers[0].Host = "10.0.0.42"
	if err := m2.Replace(&cp2); err != nil {
		t.Fatalf("阶段 4 Replace: %v", err)
	}

	// 阶段 5: 再 kill → 重启 → 验证 host + encoding 都还在
	cfg3, err := Load(path)
	if err != nil {
		t.Fatalf("阶段 5 Load 失败: %v", err)
	}
	if got := cfg3.Systems[0].Servers[0].Host; got != "10.0.0.42" {
		t.Errorf("阶段 5 host: %q, want 10.0.0.42", got)
	}
	if got := cfg3.Systems[0].Servers[0].LogDirs[0].Encoding; got != "gbk" {
		t.Errorf("阶段 5 encoding: %q, want gbk", got)
	}
}
