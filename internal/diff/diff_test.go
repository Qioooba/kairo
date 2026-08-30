package diff

import (
	"math/rand"
	"strings"
	"testing"
)

func TestCompare_RandomEditScriptReconstructsBothSides(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	alphabet := []string{"a", "b", "c", "d"}
	for sample := 0; sample < 500; sample++ {
		left := make([]string, rng.Intn(24))
		right := make([]string, rng.Intn(24))
		for i := range left {
			left[i] = alphabet[rng.Intn(len(alphabet))]
		}
		for i := range right {
			right[i] = alphabet[rng.Intn(len(alphabet))]
		}
		result := Compare(left, right, "left", "right")
		var rebuiltLeft, rebuiltRight []string
		for _, line := range result.Lines {
			if line.Op != OpInsert {
				rebuiltLeft = append(rebuiltLeft, line.Text)
			}
			if line.Op != OpDelete {
				rebuiltRight = append(rebuiltRight, line.Text)
			}
		}
		if strings.Join(rebuiltLeft, "\x00") != strings.Join(left, "\x00") || strings.Join(rebuiltRight, "\x00") != strings.Join(right, "\x00") {
			t.Fatalf("sample %d does not reconstruct inputs: left=%v/%v right=%v/%v", sample, rebuiltLeft, left, rebuiltRight, right)
		}
	}
}

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
	// 超过最大编辑距离时安全退化，但不能再分配 N*M 的矩阵。
	left := make([]string, maxMyersDistance+1)
	right := make([]string, maxMyersDistance+1)
	for i := range left {
		left[i] = "left-" + strings.Repeat("x", i%7)
		right[i] = "right-" + strings.Repeat("y", i%7)
	}
	r := Compare(left, right, "l", "r")
	if !strings.Contains(r.UnifiedDiff, "注：两侧行数过多") {
		t.Fatalf("expected truncation note in unified diff, got: %q", r.UnifiedDiff)
	}
	// 但 stats 仍应正确
	if r.Stats.LeftLines != maxMyersDistance+1 || r.Stats.RightLines != maxMyersDistance+1 {
		t.Fatalf("stats should still report original line counts, got %+v", r.Stats)
	}
}

func TestCompare_LargeIdenticalDoesNotTruncate(t *testing.T) {
	lines := make([]string, maxMyersDistance*3)
	for i := range lines {
		lines[i] = "same"
	}
	r := Compare(lines, lines, "l", "r")
	if r.Stats.Common != len(lines) || r.Stats.Added != 0 || r.Stats.Removed != 0 {
		t.Fatalf("large identical input must remain equal: %+v", r.Stats)
	}
	if strings.Contains(r.UnifiedDiff, "已退化") {
		t.Fatal("large identical input should not truncate")
	}
}

func TestCompareWithKeysPreservesOriginalTextAndLineNumbers(t *testing.T) {
	r := CompareWithKeys(
		[]string{"Foo  ", "value"}, []string{"foo", "VALUE"},
		[]string{"foo", "value"}, []string{"foo", "value"},
		[]int{1, 3}, []int{1, 2}, 3, 2, "left", "right",
	)
	if r.Stats.Common != 2 || r.Stats.LeftLines != 3 || r.Stats.RightLines != 2 {
		t.Fatalf("unexpected stats: %+v", r.Stats)
	}
	if got := r.Lines[0]; got.Text != "Foo  " || got.LeftNo != 1 || got.RightNo != 1 {
		t.Fatalf("original text/line numbers not preserved: %+v", got)
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

func BenchmarkCompareLargeMostlyEqual(b *testing.B) {
	left := make([]string, 100000)
	for i := range left {
		left[i] = "unchanged configuration line"
	}
	right := append([]string(nil), left...)
	for i := 2500; i < len(right); i += 5000 {
		right[i] = "changed configuration line"
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Compare(left, right, "left", "right")
	}
}
