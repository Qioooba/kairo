package reminder

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	good := Reminder{Type: TypeWeekly, Content: "hi", Weekdays: []int{1, 2}, Time: "17:00"}
	if err := good.Validate(); err != nil {
		t.Fatalf("good weekly validate: %v", err)
	}

	bad := []Reminder{
		{Type: "weird", Content: "x"},
		{Type: TypeOnce, Content: "x", At: "not-a-date"},
		{Type: TypeWeekly, Content: "x", Weekdays: []int{8}, Time: "17:00"},
		{Type: TypeMonthly, Content: "x", DayOfMonth: 0, Time: "17:00"},
		{Type: TypeMonthly, Content: "x", DayOfMonth: 20, Time: "25:99"},
		{Type: TypeWeekly, Content: ""},
	}
	for i, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("bad[%d] should fail: %+v", i, r)
		}
	}
}

func TestNextFireWeekly(t *testing.T) {
	// 周一 17:00，今天是周日 22:00 → 下个周一 17:00（明天）
	loc, err := time.LoadLocation("Local")
	if err != nil {
		t.Fatal(err)
	}
	// 找一个确切的"周日 22:00"
	now := time.Date(2026, 7, 12, 22, 0, 0, 0, loc) // 2026-07-12 是周日
	r := Reminder{Type: TypeWeekly, Enabled: true, Weekdays: []int{1}, Time: "17:00"}
	got := r.NextFire(now)
	want := time.Date(2026, 7, 13, 17, 0, 0, 0, loc) // 周一 17:00
	if !got.Equal(want) {
		t.Errorf("next fire (周日→周一): got %v, want %v", got, want)
	}

	// 今天是周一 9:00 → 今天 17:00
	now = time.Date(2026, 7, 13, 9, 0, 0, 0, loc)
	got = r.NextFire(now)
	want = time.Date(2026, 7, 13, 17, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("next fire (周一 9 点→周一 17 点): got %v, want %v", got, want)
	}

	// 今天是周一 18:00（已过今天）→ 下周一 17:00
	now = time.Date(2026, 7, 13, 18, 0, 0, 0, loc)
	got = r.NextFire(now)
	want = time.Date(2026, 7, 20, 17, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("next fire (周一 18 点→下周一 17 点): got %v, want %v", got, want)
	}
}

func TestNextFireMonthly(t *testing.T) {
	loc, err := time.LoadLocation("Local")
	if err != nil {
		t.Fatal(err)
	}
	// 每月 31 号 09:00；2026-02 没有 31 号 → 回退到 2 月 28 号
	r := Reminder{Type: TypeMonthly, Enabled: true, DayOfMonth: 31, Time: "09:00"}
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, loc)
	got := r.NextFire(now)
	want := time.Date(2026, 2, 28, 9, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("Feb 31 回退: got %v, want %v", got, want)
	}

	// 今天是 4/30 10:00（本月 4/30 09:00 已过）→ 下月 5/31 09:00
	now = time.Date(2026, 4, 30, 10, 0, 0, 0, loc)
	got = r.NextFire(now)
	want = time.Date(2026, 5, 31, 9, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("下月: got %v, want %v", got, want)
	}
}

func TestNextFireOnce(t *testing.T) {
	loc, err := time.LoadLocation("Local")
	if err != nil {
		t.Fatal(err)
	}
	r := Reminder{Type: TypeOnce, Enabled: true, At: "2026-07-20T15:00"}

	now := time.Date(2026, 7, 13, 10, 0, 0, 0, loc)
	got := r.NextFire(now)
	want := time.Date(2026, 7, 20, 15, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("future once: got %v, want %v", got, want)
	}

	// 已过
	now = time.Date(2026, 7, 21, 10, 0, 0, 0, loc)
	got = r.NextFire(now)
	if !got.IsZero() {
		t.Errorf("past once: should be zero, got %v", got)
	}
}

func TestStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reminders.json")
	s := NewStore(path)

	items := []Reminder{
		{ID: "a", Type: TypeWeekly, Enabled: true, Content: "写日报", Weekdays: []int{1, 2, 3, 4, 5}, Time: "17:00"},
		{ID: "b", Type: TypeOnce, Enabled: true, Content: "发布会", At: "2026-07-20T15:00"},
	}
	if err := s.Save(items); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
	if got[0].Content != "写日报" {
		t.Errorf("item 0 content: %s", got[0].Content)
	}
	if got[1].At != "2026-07-20T15:00" {
		t.Errorf("item 1 at: %s", got[1].At)
	}
}

func TestStoreCorruptBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reminders.json")
	if err := os.WriteFile(path, []byte("{garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	got, err := s.Load()
	if err == nil {
		t.Error("expected error for corrupt JSON")
	}
	if len(got) != 0 {
		t.Errorf("expected empty list on corrupt, got %d", len(got))
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("expected .bak backup, got %v", err)
	}
}

func TestManagerCRUD(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "reminders.json"))

	var mu sync.Mutex
	var firedCount int
	var lastFired Reminder
	m, err := NewManager(s, func(r Reminder, at time.Time) {
		mu.Lock()
		defer mu.Unlock()
		firedCount++
		lastFired = r
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	// Add
	in := Reminder{Type: TypeWeekly, Enabled: true, Content: "周报", Weekdays: []int{5}, Time: "17:00"}
	out, err := m.Add(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID == "" {
		t.Error("ID not assigned")
	}

	// Get
	got, ok := m.Get(out.ID)
	if !ok || got.Content != "周报" {
		t.Errorf("Get: %+v ok=%v", got, ok)
	}

	// Update
	out.Content = "周五 17:00 写周报"
	updated, err := m.Update(out.ID, out)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Content != "周五 17:00 写周报" {
		t.Error("update not persisted")
	}

	// Toggle
	toggled, err := m.Toggle(out.ID)
	if err != nil || toggled.Enabled {
		t.Errorf("toggle: %+v err=%v", toggled, err)
	}

	// Delete
	if err := m.Delete(out.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get(out.ID); ok {
		t.Error("still exists after delete")
	}

	// FireNow
	in2 := Reminder{Type: TypeOnce, Enabled: true, Content: "test fire", At: "2099-01-01T10:00"}
	out2, _ := m.Add(in2)
	if err := m.FireNow(out2.ID); err != nil {
		t.Fatal(err)
	}
	// OnFire 是异步触发，等一下
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		fc := firedCount
		mu.Unlock()
		if fc > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	fc := firedCount
	lf := lastFired
	mu.Unlock()
	if fc != 1 {
		t.Errorf("fired count: got %d, want 1", fc)
	}
	if lf.Content != "test fire" {
		t.Errorf("fired content: %s", lf.Content)
	}
}

func TestPauseUntil(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "reminders.json"))

	var firedCount int
	m, err := NewManager(s, func(r Reminder, at time.Time) {
		firedCount++
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	// 默认未暂停
	paused, until := m.PauseStatus()
	if paused || until != "" {
		t.Errorf("initial pause status: paused=%v until=%q", paused, until)
	}

	// 暂停到未来 1 小时
	future := time.Now().Add(time.Hour)
	m.PauseUntil(future)
	paused, until = m.PauseStatus()
	if !paused {
		t.Error("expected paused after PauseUntil(future)")
	}

	// 解析 until 回 RFC3339
	parsed, err := time.Parse(time.RFC3339, until)
	if err != nil {
		t.Errorf("until not RFC3339: %q", until)
	}
	// RFC3339 秒级精度，容忍毫秒差异
	if parsed.Truncate(time.Second) != future.Truncate(time.Second) {
		t.Errorf("until mismatch: got %v, want %v", parsed, future)
	}

	// FireNow 在暂停期不该真的 fire 到 OnFire——但 FireNow 直接调 onFire，
	// 所以这条不影响 Pause 机制。Pause 机制影响的是 fire() 内部的 due 检测。

	// 解除暂停
	m.PauseUntil(time.Time{})
	paused, _ = m.PauseStatus()
	if paused {
		t.Error("expected not paused after resume")
	}

	// 已过的暂停时间应自动判定为非暂停
	past := time.Now().Add(-time.Hour)
	m.PauseUntil(past)
	paused, _ = m.PauseStatus()
	if paused {
		t.Error("past pause should not be active")
	}
}

func TestDueAt(t *testing.T) {
	loc := time.Local

	// once: 已到点 → 返回 at
	r1 := Reminder{Type: TypeOnce, Enabled: true, At: "2026-07-20T15:00"}
	now := time.Date(2026, 7, 20, 15, 0, 0, 0, loc)
	got := r1.DueAt(now)
	want := time.Date(2026, 7, 20, 15, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("once due: got %v, want %v", got, want)
	}
	// once: 还没到点 → 零值
	now = time.Date(2026, 7, 20, 14, 59, 0, 0, loc)
	if got := r1.DueAt(now); !got.IsZero() {
		t.Errorf("once not yet due: should be zero, got %v", got)
	}

	// weekly: 今天是周一 18:00（17:00 已过）→ 今天 17:00
	r2 := Reminder{Type: TypeWeekly, Enabled: true, Weekdays: []int{1}, Time: "17:00"}
	now = time.Date(2026, 7, 13, 18, 0, 0, 0, loc) // 2026-07-13 周一
	got = r2.DueAt(now)
	want = time.Date(2026, 7, 13, 17, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("weekly due (今天已过): got %v, want %v", got, want)
	}
	// weekly: 今天是周一 16:00（还没到 17:00）→ 上周一 17:00
	now = time.Date(2026, 7, 13, 16, 0, 0, 0, loc)
	got = r2.DueAt(now)
	want = time.Date(2026, 7, 6, 17, 0, 0, 0, loc) // 上周一
	if !got.Equal(want) {
		t.Errorf("weekly due (今天未到): got %v, want %v", got, want)
	}

	// monthly: 本月 8 号 10:05（10:00 已过）→ 本月 8 号 10:00
	r3 := Reminder{Type: TypeMonthly, Enabled: true, DayOfMonth: 8, Time: "10:00"}
	now = time.Date(2026, 7, 8, 10, 5, 0, 0, loc)
	got = r3.DueAt(now)
	want = time.Date(2026, 7, 8, 10, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("monthly due (本月已过): got %v, want %v", got, want)
	}
	// monthly: 本月 8 号 09:00（还没到 10:00）→ 上月 8 号 10:00
	now = time.Date(2026, 7, 8, 9, 0, 0, 0, loc)
	got = r3.DueAt(now)
	want = time.Date(2026, 6, 8, 10, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("monthly due (本月未到): got %v, want %v", got, want)
	}
}

// TestFireScheduledTriggersOnFire 验证定时到点 fire() 能真正触发 onFire。
// 这是 v0.x 修复的核心 bug：旧 fire() 用 NextFire(now)（只返回未来）判 due，
// 导致定时提醒永远不弹窗（只有"测试"按钮 FireNow 能弹）。
func TestFireScheduledTriggersOnFire(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "reminders.json"))

	var mu sync.Mutex
	var fired []Reminder
	m, err := NewManager(s, func(r Reminder, at time.Time) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, r)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	// 注入固定 now：周一 17:00:00（恰好到点）
	slot := time.Date(2026, 7, 13, 17, 0, 0, 0, time.Local) // 2026-07-13 周一
	m.now = func() time.Time { return slot }

	r, err := m.Add(Reminder{Type: TypeWeekly, Enabled: true, Content: "周报", Weekdays: []int{1}, Time: "17:00"})
	if err != nil {
		t.Fatal(err)
	}

	// 直接调 fire()，模拟 timer 到点
	m.fire()

	// onFire 异步触发，等一下
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(fired)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	if len(fired) != 1 {
		t.Fatalf("expected 1 fire, got %d", len(fired))
	}
	if fired[0].Content != "周报" {
		t.Errorf("fired content: %s", fired[0].Content)
	}
	mu.Unlock()

	// 验证 LastFiredAt 已更新、FiredCount 递增
	got, _ := m.Get(r.ID)
	if got.FiredCount != 1 {
		t.Errorf("FiredCount: got %d, want 1", got.FiredCount)
	}
	if got.LastFiredAt == "" {
		t.Error("LastFiredAt should be set")
	}
}

// TestFireOnceTriggersOnFire 验证单次提醒到点也能触发。
func TestFireOnceTriggersOnFire(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "reminders.json"))

	var mu sync.Mutex
	var firedCount int
	m, err := NewManager(s, func(r Reminder, at time.Time) {
		mu.Lock()
		defer mu.Unlock()
		firedCount++
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	slot := time.Date(2026, 7, 20, 15, 0, 0, 0, time.Local)
	m.now = func() time.Time { return slot }

	r, err := m.Add(Reminder{Type: TypeOnce, Enabled: true, Content: "发布会", At: "2026-07-20T15:00"})
	if err != nil {
		t.Fatal(err)
	}

	m.fire()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		if firedCount > 0 {
			mu.Unlock()
			break
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	if firedCount != 1 {
		t.Fatalf("expected 1 fire, got %d", firedCount)
	}
	mu.Unlock()

	// 单次提醒触发后应自动 disabled
	got, _ := m.Get(r.ID)
	if got.Enabled {
		t.Error("once reminder should be disabled after fire")
	}
}

// TestFireNoDoubleFire 验证同一槽位不会因 fire() 被多次调用而重复弹窗。
func TestFireNoDoubleFire(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "reminders.json"))

	var mu sync.Mutex
	var firedCount int
	m, err := NewManager(s, func(r Reminder, at time.Time) {
		mu.Lock()
		defer mu.Unlock()
		firedCount++
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	slot := time.Date(2026, 7, 13, 17, 0, 0, 0, time.Local) // 周一 17:00
	m.now = func() time.Time { return slot }

	_, err = m.Add(Reminder{Type: TypeWeekly, Enabled: true, Content: "周报", Weekdays: []int{1}, Time: "17:00"})
	if err != nil {
		t.Fatal(err)
	}

	// 连续调两次 fire()，第二次应被 LastFiredAt 防重复逻辑跳过
	m.fire()
	m.fire()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		if firedCount >= 2 {
			mu.Unlock()
			break
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	if firedCount != 1 {
		t.Errorf("expected 1 fire (no double), got %d", firedCount)
	}
	mu.Unlock()
}

// TestFireSkipsStaleSlot 验证超过容差窗口的迟到不补触发
// （模拟电脑休眠很久刚醒，避免积压提醒轰炸）。
func TestFireSkipsStaleSlot(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "reminders.json"))

	var mu sync.Mutex
	var firedCount int
	m, err := NewManager(s, func(r Reminder, at time.Time) {
		mu.Lock()
		defer mu.Unlock()
		firedCount++
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	// now 比槽时间晚 3 小时（远超 fireCatchUpWindow=2min）
	slot := time.Date(2026, 7, 13, 20, 0, 0, 0, time.Local) // 周一 20:00
	m.now = func() time.Time { return slot }

	_, err = m.Add(Reminder{Type: TypeWeekly, Enabled: true, Content: "周报", Weekdays: []int{1}, Time: "17:00"})
	if err != nil {
		t.Fatal(err)
	}

	m.fire()

	// 给异步 onFire 一点时间确认不会触发
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	if firedCount != 0 {
		t.Errorf("expected 0 fire (stale slot skipped), got %d", firedCount)
	}
	mu.Unlock()
}

// ---------- cron / lead / action（v1.1 扩展） ----------

func TestValidateCronLeadAction(t *testing.T) {
	// 合法 cron
	good := Reminder{Type: TypeCron, Enabled: true, Content: "工作日早会", Cron: "0 9 * * 1-5", LeadMinutes: 10}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid cron: %v", err)
	}
	// 合法 action
	good.Action = &Action{Kind: ActionURL, URL: "https://example.com/meeting"}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid url action: %v", err)
	}
	good.Action = &Action{Kind: ActionCommand, Command: "/usr/local/bin/notify", Args: []string{"-t", "hi"}, WorkDir: "/tmp"}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid command action: %v", err)
	}
	// nil action / 空 kind 视为 popup
	if err := (&Reminder{Type: TypeOnce, Content: "x", At: "2099-01-01T10:00", Action: &Action{}}).Validate(); err != nil {
		t.Errorf("empty action should be popup: %v", err)
	}

	bad := []Reminder{
		{Type: TypeCron, Content: "x", Cron: ""},
		{Type: TypeCron, Content: "x", Cron: "* * * *"},
		{Type: TypeCron, Content: "x", Cron: "@every 5m"},
		{Type: TypeOnce, Content: "x", At: "2099-01-01T10:00", LeadMinutes: -1},
		{Type: TypeOnce, Content: "x", At: "2099-01-01T10:00", LeadMinutes: 1500},
		{Type: TypeOnce, Content: "x", At: "2099-01-01T10:00", Action: &Action{Kind: "teleport"}},
		{Type: TypeOnce, Content: "x", At: "2099-01-01T10:00", Action: &Action{Kind: ActionURL, URL: "ftp://x"}},
		{Type: TypeOnce, Content: "x", At: "2099-01-01T10:00", Action: &Action{Kind: ActionURL, URL: "notaurl"}},
		{Type: TypeOnce, Content: "x", At: "2099-01-01T10:00", Action: &Action{Kind: ActionURL}},
		{Type: TypeOnce, Content: "x", At: "2099-01-01T10:00", Action: &Action{Kind: ActionCommand}},
	}
	for i, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("bad[%d] should fail: %+v", i, r)
		}
	}
}

func TestNextFireCron(t *testing.T) {
	loc := time.Local
	r := Reminder{Type: TypeCron, Enabled: true, Content: "早会", Cron: "0 9 * * 1-5"}

	// 周一 08:00 → 周一 09:00
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, loc) // 2026-07-13 周一
	want := time.Date(2026, 7, 13, 9, 0, 0, 0, loc)
	if got := r.NextFire(now); !got.Equal(want) {
		t.Errorf("cron next (周一8点): got %v, want %v", got, want)
	}

	// 周一 10:00（今天 9 点已过）→ 周二 09:00
	now = time.Date(2026, 7, 13, 10, 0, 0, 0, loc)
	want = time.Date(2026, 7, 14, 9, 0, 0, 0, loc)
	if got := r.NextFire(now); !got.Equal(want) {
		t.Errorf("cron next (周一10点): got %v, want %v", got, want)
	}

	// 周五 10:00 → 下周一 09:00
	now = time.Date(2026, 7, 17, 10, 0, 0, 0, loc) // 2026-07-17 周五
	want = time.Date(2026, 7, 20, 9, 0, 0, 0, loc)
	if got := r.NextFire(now); !got.Equal(want) {
		t.Errorf("cron next (周五10点): got %v, want %v", got, want)
	}
}

func TestNextFireCronWithLead(t *testing.T) {
	loc := time.Local
	r := Reminder{Type: TypeCron, Enabled: true, Content: "早会", Cron: "0 9 * * 1-5", LeadMinutes: 10}

	// 周一 08:00 → fire 周一 08:50
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, loc)
	want := time.Date(2026, 7, 13, 8, 50, 0, 0, loc)
	if got := r.NextFire(now); !got.Equal(want) {
		t.Errorf("cron+lead next (周一8点): got %v, want %v", got, want)
	}

	// 周一 08:55（fire 时刻 08:50 已过）→ 周二 08:50
	now = time.Date(2026, 7, 13, 8, 55, 0, 0, loc)
	want = time.Date(2026, 7, 14, 8, 50, 0, 0, loc)
	if got := r.NextFire(now); !got.Equal(want) {
		t.Errorf("cron+lead next (周一8:55): got %v, want %v", got, want)
	}
}

func TestDueAtCronWithLead(t *testing.T) {
	loc := time.Local
	// 每 5 分钟一次，提前 10 分钟：match 09:15 → fire 09:05
	r := Reminder{Type: TypeCron, Enabled: true, Content: "打卡", Cron: "*/5 * * * *", LeadMinutes: 10}

	// fire 时刻 09:05:00 刚到 → 返回 09:05
	now := time.Date(2026, 7, 13, 9, 5, 0, 0, loc)
	want := time.Date(2026, 7, 13, 9, 5, 0, 0, loc)
	if got := r.DueAt(now); !got.Equal(want) {
		t.Errorf("cron due (9:05): got %v, want %v", got, want)
	}

	// fire 时刻 09:05:30（抖动 30s，容差内）→ 仍返回 09:05
	now = time.Date(2026, 7, 13, 9, 5, 30, 0, loc)
	if got := r.DueAt(now); !got.Equal(want) {
		t.Errorf("cron due (9:05:30): got %v, want %v", got, want)
	}

	// 09:08：最近槽 09:15 仍在回看窗口内 → 返回 fire 09:05（陈旧值，
	// 由 fire() 的容差窗口 diff=3min>2min 过滤，不会真触发）
	now = time.Date(2026, 7, 13, 9, 8, 0, 0, loc)
	if got := r.DueAt(now); !got.Equal(want) {
		t.Errorf("cron due (9:08): got %v, want %v", got, want)
	}

	// 09:09：最近槽 09:15 超出 3 分钟回看窗口 → 零值
	now = time.Date(2026, 7, 13, 9, 9, 0, 0, loc)
	if got := r.DueAt(now); !got.IsZero() {
		t.Errorf("cron due (9:09) should be zero, got %v", got)
	}
}

func TestNextFireOnceWithLead(t *testing.T) {
	loc := time.Local
	r := Reminder{Type: TypeOnce, Enabled: true, Content: "发布会", At: "2026-07-20T15:00", LeadMinutes: 10}

	// 会前 10 分钟：fire 14:50
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, loc)
	want := time.Date(2026, 7, 20, 14, 50, 0, 0, loc)
	if got := r.NextFire(now); !got.Equal(want) {
		t.Errorf("once+lead next: got %v, want %v", got, want)
	}

	// 14:55（fire 时刻 14:50 已过）→ 零值
	now = time.Date(2026, 7, 20, 14, 55, 0, 0, loc)
	if got := r.NextFire(now); !got.IsZero() {
		t.Errorf("once+lead next (已过): should be zero, got %v", got)
	}

	// DueAt：14:50 到点
	now = time.Date(2026, 7, 20, 14, 50, 0, 0, loc)
	if got := r.DueAt(now); !got.Equal(want) {
		t.Errorf("once+lead due: got %v, want %v", got, want)
	}
}

func TestNextFireWeeklyWithLead(t *testing.T) {
	loc := time.Local
	r := Reminder{Type: TypeWeekly, Enabled: true, Content: "周会", Weekdays: []int{3}, Time: "10:00", LeadMinutes: 30}

	// 周二 09:00 → fire 周三 09:30
	now := time.Date(2026, 7, 14, 9, 0, 0, 0, loc) // 2026-07-14 周二
	want := time.Date(2026, 7, 15, 9, 30, 0, 0, loc)
	if got := r.NextFire(now); !got.Equal(want) {
		t.Errorf("weekly+lead next (周二9点): got %v, want %v", got, want)
	}

	// 周三 09:40（fire 时刻 09:30 已过）→ 下周三 09:30
	now = time.Date(2026, 7, 15, 9, 40, 0, 0, loc) // 2026-07-15 周三
	want = time.Date(2026, 7, 22, 9, 30, 0, 0, loc)
	if got := r.NextFire(now); !got.Equal(want) {
		t.Errorf("weekly+lead next (周三9:40): got %v, want %v", got, want)
	}

	// 周三 09:20（今天 fire 时刻 09:30 还没到）→ 今天 09:30
	now = time.Date(2026, 7, 15, 9, 20, 0, 0, loc)
	want = time.Date(2026, 7, 15, 9, 30, 0, 0, loc)
	if got := r.NextFire(now); !got.Equal(want) {
		t.Errorf("weekly+lead next (周三9:20): got %v, want %v", got, want)
	}
}

func TestHumanScheduleCronAndLead(t *testing.T) {
	r := Reminder{Type: TypeCron, Cron: "0 9 * * 1-5"}
	if got := r.HumanSchedule(); got != "Cron: 0 9 * * 1-5" {
		t.Errorf("cron schedule: %q", got)
	}
	r.LeadMinutes = 10
	if got := r.HumanSchedule(); got != "Cron: 0 9 * * 1-5 · 提前 10 分钟" {
		t.Errorf("cron+lead schedule: %q", got)
	}
	r2 := Reminder{Type: TypeOnce, At: "2026-07-20T15:00", LeadMinutes: 5}
	if got := r2.HumanSchedule(); got != "2026-07-20 15:00 · 提前 5 分钟" {
		t.Errorf("once+lead schedule: %q", got)
	}
}

// TestFireCronTriggersOnFire 验证 cron 提醒到点也能触发（含 lead 提前量）。
func TestFireCronTriggersOnFire(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "reminders.json"))

	var mu sync.Mutex
	var fired []Reminder
	m, err := NewManager(s, func(r Reminder, at time.Time) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, r)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	// match 周一 09:00，lead 10min → fire 周一 08:50
	slot := time.Date(2026, 7, 13, 8, 50, 0, 0, time.Local) // 2026-07-13 周一
	m.now = func() time.Time { return slot }

	r, err := m.Add(Reminder{Type: TypeCron, Enabled: true, Content: "早会", Cron: "0 9 * * 1-5", LeadMinutes: 10})
	if err != nil {
		t.Fatal(err)
	}

	m.fire()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(fired)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	if len(fired) != 1 {
		t.Fatalf("expected 1 fire, got %d", len(fired))
	}
	if fired[0].Content != "早会" {
		t.Errorf("fired content: %s", fired[0].Content)
	}
	mu.Unlock()

	// cron 提醒触发后不应 disabled（周期提醒持续有效）
	got, _ := m.Get(r.ID)
	if !got.Enabled {
		t.Error("cron reminder should stay enabled after fire")
	}
	if got.FiredCount != 1 {
		t.Errorf("FiredCount: got %d, want 1", got.FiredCount)
	}
}

// TestStoreRoundtripCronLeadAction 验证新字段 JSON 持久化不丢。
func TestStoreRoundtripCronLeadAction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reminders.json")
	s := NewStore(path)

	items := []Reminder{{
		ID:          "c1",
		Type:        TypeCron,
		Enabled:     true,
		Content:     "早会",
		Cron:        "0 9 * * 1-5",
		LeadMinutes: 10,
		Action:      &Action{Kind: ActionCommand, Command: "/usr/bin/say", Args: []string{"早会啦"}, WorkDir: "/tmp"},
	}}
	if err := s.Save(items); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	g := got[0]
	if g.Cron != "0 9 * * 1-5" || g.LeadMinutes != 10 {
		t.Errorf("cron/lead lost: %+v", g)
	}
	if g.Action == nil || g.Action.Kind != ActionCommand || g.Action.Command != "/usr/bin/say" || len(g.Action.Args) != 1 {
		t.Errorf("action lost: %+v", g.Action)
	}
}
