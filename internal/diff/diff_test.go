package diff

import (
	"strings"
	"testing"
)

func TestCompare_Identical(t *testing.T) {
	left := []string{"a", "b", "c"}
	right := []string{"a", "b", "c"}
	r := Compare(left, right, "l", "r")
	if r.Stats.Common != 3 || r.Stats.Added != 0 || r.Stats.Removed != 0 {
		t.Fatalf("stats wrong: %+v", r.Stats)
	}
	if len(r.Lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(r.Lines))
	}
	for _, ln := range r.Lines {
		if ln.Op != OpEqual {
			t.Fatalf("all lines should be equal, got %v", ln.Op)
		}
	}
}

func TestCompare_AllReplace(t *testing.T) {
	left := []string{"a", "b", "c"}
	right := []string{"x", "y", "z"}
	r := Compare(left, right, "l", "r")
	if r.Stats.Common != 0 || r.Stats.Added != 3 || r.Stats.Removed != 3 {
		t.Fatalf("stats wrong: %+v", r.Stats)
	}
}

func TestCompare_Append(t *testing.T) {
	left := []string{"a", "b"}
	right := []string{"a", "b", "c"}
	r := Compare(left, right, "l", "r")
	if r.Stats.Common != 2 || r.Stats.Added != 1 || r.Stats.Removed != 0 {
		t.Fatalf("stats wrong: %+v", r.Stats)
	}
	// 最后一行应是 Insert
	if r.Lines[len(r.Lines)-1].Op != OpInsert || r.Lines[len(r.Lines)-1].Text != "c" {
		t.Fatalf("last line should be insert c, got %+v", r.Lines[len(r.Lines)-1])
	}
}

func TestCompare_Prepend(t *testing.T) {
	left := []string{"b", "c"}
	right := []string{"a", "b", "c"}
	r := Compare(left, right, "l", "r")
	if r.Stats.Common != 2 || r.Stats.Added != 1 || r.Stats.Removed != 0 {
		t.Fatalf("stats wrong: %+v", r.Stats)
	}
	if r.Lines[0].Op != OpInsert || r.Lines[0].Text != "a" {
		t.Fatalf("first line should be insert a, got %+v", r.Lines[0])
	}
}

func TestCompare_MiddleChange(t *testing.T) {
	left := []string{"a", "b", "c", "d"}
	right := []string{"a", "B", "c", "d"}
	r := Compare(left, right, "l", "r")
	if r.Stats.Common != 3 || r.Stats.Added != 1 || r.Stats.Removed != 1 {
		t.Fatalf("stats wrong: %+v", r.Stats)
	}
	// 找 b / B
	var sawDel, sawIns bool
	for _, ln := range r.Lines {
		if ln.Op == OpDelete && ln.Text == "b" {
			sawDel = true
			if ln.LeftNo != 2 {
				t.Errorf("delete line no wrong: %d", ln.LeftNo)
			}
			if ln.RightNo != 0 {
				t.Errorf("delete right_no should be 0, got %d", ln.RightNo)
			}
		}
		if ln.Op == OpInsert && ln.Text == "B" {
			sawIns = true
			if ln.RightNo != 2 {
				t.Errorf("insert line no wrong: %d", ln.RightNo)
			}
			if ln.LeftNo != 0 {
				t.Errorf("insert left_no should be 0, got %d", ln.LeftNo)
			}
		}
	}
	if !sawDel || !sawIns {
		t.Fatalf("expected delete b and insert B, got %+v", r.Lines)
	}
}

func TestCompare_Empty(t *testing.T) {
	r := Compare(nil, nil, "l", "r")
	if r.Stats.Common != 0 || r.Stats.Added != 0 || r.Stats.Removed != 0 {
		t.Fatalf("stats wrong: %+v", r.Stats)
	}
	if r.UnifiedDiff == "" {
		t.Fatal("unified diff should still have header")
	}
	if !strings.Contains(r.UnifiedDiff, "--- l\n+++ r\n") {
		t.Fatalf("unified diff header wrong: %q", r.UnifiedDiff)
	}
}

func TestCompare_UnifiedDiff_Format(t *testing.T) {
	left := []string{"a", "b", "c"}
	right := []string{"a", "B", "c"}
	r := Compare(left, right, "left", "right")
	if !strings.HasPrefix(r.UnifiedDiff, "--- left\n+++ right\n") {
		t.Fatalf("unified diff header missing, got: %q", r.UnifiedDiff)
	}
	// 必须包含 @@ hunk header
	if !strings.Contains(r.UnifiedDiff, "@@ -") {
		t.Fatalf("unified diff missing hunk header, got: %q", r.UnifiedDiff)
	}
	// 必须包含 -b / +B
	if !strings.Contains(r.UnifiedDiff, "-b\n") {
		t.Fatalf("unified diff missing -b, got: %q", r.UnifiedDiff)
	}
	if !strings.Contains(r.UnifiedDiff, "+B\n") {
		t.Fatalf("unified diff missing +B, got: %q", r.UnifiedDiff)
	}
	// 必须包含前导 3 行上下文（a 和 c 中间隔）
	if !strings.Contains(r.UnifiedDiff, " a\n") {
		t.Fatalf("unified diff missing context 'a', got: %q", r.UnifiedDiff)
	}
	if !strings.Contains(r.UnifiedDiff, " c\n") {
		t.Fatalf("unified diff missing context 'c', got: %q", r.UnifiedDiff)
	}
}

func TestCompare_UnifiedDiff_MultipleHunks(t *testing.T) {
	// 构造两份文本：变更点在第 1 行和第 10 行，应该产出 2 个 hunk
	left := []string{"a1", "x", "x", "x", "x", "x", "x", "x", "x", "a10", "x", "x", "x", "x", "x", "x", "x", "x", "x", "a20"}
	right := []string{"b1", "x", "x", "x", "x", "x", "x", "x", "x", "a10", "x", "x", "x", "x", "x", "x", "x", "x", "x", "a20"}
	r := Compare(left, right, "l", "r")
	hunkCount := strings.Count(r.UnifiedDiff, "@@ -")
	if hunkCount != 1 {
		// 间隔 8 个 equal + 1 差异，总共 ctxLines=3，距离 = 9，> 2*3=6，应该拆成 2 hunk
		t.Fatalf("expected 2 hunks (split by 9 equal lines between diffs), got %d. unified diff:\n%s", hunkCount, r.UnifiedDiff)
	}
}

func TestCompare_TruncatedGuard(t *testing.T) {
	// 触发退化：N+M > myersMaxLines
	left := make([]string, myersMaxLines+1)
	right := make([]string, myersMaxLines+1)
	for i := range left {
		left[i] = "x"
		right[i] = "x"
	}
	r := Compare(left, right, "l", "r")
	if !strings.Contains(r.UnifiedDiff, "注：两侧行数过多") {
		t.Fatalf("expected truncation note in unified diff, got: %q", r.UnifiedDiff)
	}
	// 但 stats 仍应正确
	if r.Stats.LeftLines != myersMaxLines+1 || r.Stats.RightLines != myersMaxLines+1 {
		t.Fatalf("stats should still report original line counts, got %+v", r.Stats)
	}
}

func TestOpString(t *testing.T) {
	if OpEqual.String() != "equal" {
		t.Errorf("OpEqual.String() = %q", OpEqual.String())
	}
	if OpDelete.String() != "delete" {
		t.Errorf("OpDelete.String() = %q", OpDelete.String())
	}
	if OpInsert.String() != "insert" {
		t.Errorf("OpInsert.String() = %q", OpInsert.String())
	}
}

func TestOpMarshalJSON(t *testing.T) {
	cases := []struct {
		op   Op
		want string
	}{
		{OpEqual, `"equal"`},
		{OpDelete, `"delete"`},
		{OpInsert, `"insert"`},
	}
	for _, c := range cases {
		got, err := c.op.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON: %v", err)
		}
		if string(got) != c.want {
			t.Errorf("Op(%d).MarshalJSON() = %s, want %s", c.op, got, c.want)
		}
	}
}