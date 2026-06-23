package logquery

import (
	"testing"
	"time"
)

// TestSearchTimeWindow_Contains 验证窗口边界语义（B1）。
//
// 规则：
//   - Start 零值 = 不限起点；
//   - End 零值 = 不限终点；
//   - 都零值 = 全包含；
//   - 都非零时使用闭区间 [Start, End]；
//   - 时区无关：用同一 time.Time 绝对值比较。
func TestSearchTimeWindow_Contains(t *testing.T) {
	base := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		win  SearchTimeWindow
		t    time.Time
		want bool
	}{
		{name: "全零 → 全包含", win: SearchTimeWindow{}, t: base, want: true},
		{name: "仅 Start，t > Start", win: SearchTimeWindow{Start: base}, t: base.Add(time.Hour), want: true},
		{name: "仅 Start，t == Start（闭）", win: SearchTimeWindow{Start: base}, t: base, want: true},
		{name: "仅 Start，t < Start", win: SearchTimeWindow{Start: base}, t: base.Add(-time.Hour), want: false},
		{name: "仅 End，t < End", win: SearchTimeWindow{End: base}, t: base.Add(-time.Hour), want: true},
		{name: "仅 End，t == End（闭）", win: SearchTimeWindow{End: base}, t: base, want: true},
		{name: "仅 End，t > End", win: SearchTimeWindow{End: base}, t: base.Add(time.Hour), want: false},
		{name: "两端都在窗口内", win: SearchTimeWindow{Start: base, End: base.Add(2 * time.Hour)}, t: base.Add(time.Hour), want: true},
		{name: "t == Start（闭）", win: SearchTimeWindow{Start: base, End: base.Add(time.Hour)}, t: base, want: true},
		{name: "t == End（闭）", win: SearchTimeWindow{Start: base, End: base.Add(time.Hour)}, t: base.Add(time.Hour), want: true},
		{name: "t < Start", win: SearchTimeWindow{Start: base, End: base.Add(time.Hour)}, t: base.Add(-time.Second), want: false},
		{name: "t > End", win: SearchTimeWindow{Start: base, End: base.Add(time.Hour)}, t: base.Add(time.Hour + time.Second), want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.win.Contains(c.t); got != c.want {
				t.Errorf("Contains() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestSearchTimeWindow_IsZero 验证"双零"判定。
func TestSearchTimeWindow_IsZero(t *testing.T) {
	base := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	if !(SearchTimeWindow{}).IsZero() {
		t.Error("empty window should be zero")
	}
	if (SearchTimeWindow{Start: base}).IsZero() {
		t.Error("window with Start should not be zero")
	}
	if (SearchTimeWindow{End: base}).IsZero() {
		t.Error("window with End should not be zero")
	}
}

// TestFilterHitsByTimeWindow 验证按文件 mtime 过滤命中（B1）。
//
// 场景：3 个 hit 来自 2 个文件（a.log / b.log），a.modtime 早，b.modtime 晚。
//   - 窗口包含 a → 2 hit（a 的 2 行）；
//   - 窗口只包含 b → 1 hit；
//   - 窗口全零 → 全部 3 hit；
//   - 文件列表为空 → 全部保留（向后兼容）；
//   - hit 里的 file 不在文件列表 → 兜底保留。
func TestFilterHitsByTimeWindow(t *testing.T) {
	aTime := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)
	bTime := time.Date(2026, 6, 23, 14, 0, 0, 0, time.UTC)
	files := []FileEntry{
		{Name: "a.log", ModTime: aTime.Format(time.RFC3339)},
		{Name: "b.log", ModTime: bTime.Format(time.RFC3339)},
	}
	hits := []SearchHit{
		{File: "a.log", LineNo: 1, Content: "x1"},
		{File: "a.log", LineNo: 2, Content: "x2"},
		{File: "b.log", LineNo: 1, Content: "y1"},
	}

	cases := []struct {
		name string
		win  SearchTimeWindow
		want []string // File 字段顺序
	}{
		{
			name: "全零窗口保留全部",
			win:  SearchTimeWindow{},
			want: []string{"a.log", "a.log", "b.log"},
		},
		{
			name: "窗口包含 a 但不包含 b",
			win:  SearchTimeWindow{Start: aTime.Add(-time.Hour), End: aTime.Add(time.Hour)},
			want: []string{"a.log", "a.log"},
		},
		{
			name: "窗口只包含 b",
			win:  SearchTimeWindow{Start: bTime.Add(-time.Hour), End: bTime.Add(time.Hour)},
			want: []string{"b.log"},
		},
		{
			name: "窗口包含两端",
			win:  SearchTimeWindow{Start: aTime, End: bTime},
			want: []string{"a.log", "a.log", "b.log"},
		},
		{
			name: "窗口在 a 之前（无命中）",
			win:  SearchTimeWindow{End: aTime.Add(-time.Hour)},
			want: []string{},
		},
		{
			name: "窗口在 b 之后（无命中）",
			win:  SearchTimeWindow{Start: bTime.Add(time.Hour)},
			want: []string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FilterHitsByTimeWindow(hits, files, c.win)
			if len(got) != len(c.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i].File != c.want[i] {
					t.Errorf("[%d] File = %q, want %q", i, got[i].File, c.want[i])
				}
			}
		})
	}
}

// TestFilterHitsByTimeWindow_NoFiles 验证文件列表为空时不过滤（向后兼容）。
func TestFilterHitsByTimeWindow_NoFiles(t *testing.T) {
	hits := []SearchHit{{File: "a.log", LineNo: 1, Content: "x"}}
	got := FilterHitsByTimeWindow(hits, nil, SearchTimeWindow{Start: time.Now(), End: time.Now()})
	if len(got) != 1 {
		t.Errorf("files=nil should keep all hits; got len=%d", len(got))
	}
}

// TestFilterHitsByTimeWindow_UnknownFile 验证 hit 中的 file 不在文件列表时兜底保留。
func TestFilterHitsByTimeWindow_UnknownFile(t *testing.T) {
	aTime := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)
	files := []FileEntry{{Name: "a.log", ModTime: aTime.Format(time.RFC3339)}}
	hits := []SearchHit{
		{File: "a.log", LineNo: 1, Content: "x"},
		{File: "ghost.log", LineNo: 1, Content: "y"}, // 不在 files
	}
	// 窗口只覆盖 aTime，不覆盖 ghost（mtime=零值）
	win := SearchTimeWindow{Start: aTime.Add(-time.Hour), End: aTime.Add(time.Hour)}
	got := FilterHitsByTimeWindow(hits, files, win)
	if len(got) != 2 {
		t.Fatalf("unknown file should fallback-keep; got len=%d, files=%v", len(got), got)
	}
}

// TestFileEntry_ModTimeParsed 验证 RFC3339 字符串解析（B1 用）。
func TestFileEntry_ModTimeParsed(t *testing.T) {
	e := FileEntry{ModTime: "2026-06-23T14:00:00Z"}
	got := e.ModTimeParsed()
	want := time.Date(2026, 6, 23, 14, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("ModTimeParsed() = %v, want %v", got, want)
	}
	if e := (FileEntry{ModTime: ""}).ModTimeParsed(); !e.IsZero() {
		t.Errorf("empty ModTime should parse to zero time, got %v", e)
	}
	if e := (FileEntry{ModTime: "not a time"}).ModTimeParsed(); !e.IsZero() {
		t.Errorf("garbage ModTime should parse to zero time, got %v", e)
	}
}
