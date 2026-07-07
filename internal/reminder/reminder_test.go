package reminder

import (
	"os"
	"path/filepath"
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

	var firedCount int
	var lastFired Reminder
	m, err := NewManager(s, func(r Reminder, at time.Time) {
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
	for time.Now().Before(deadline) && firedCount == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if firedCount != 1 {
		t.Errorf("fired count: got %d, want 1", firedCount)
	}
	if lastFired.Content != "test fire" {
		t.Errorf("fired content: %s", lastFired.Content)
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