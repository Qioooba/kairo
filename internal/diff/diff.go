// Package diff 提供行级文本比对（Myers diff 算法）。
//
// 设计要点：
//   - 输入是已经规范化（按 Ignore 配置处理过）的两份 []string；
//     是否忽略空行 / 空白 / 大小写由 handler 层决定，本包只做最纯粹的 LCS-based diff。
//   - 输出 Lines 同时给出 left_no / right_no，方便前端做 side-by-side 渲染。
//   - 同时输出 UnifiedDiff 字符串，方便前端"复制 diff / 下载 .diff 文件"。
//   - 算法：经典 Myers O(ND)，最长公共子序列，回溯得到编辑脚本。
//     单文件场景（< 4MB），N+M 几万行以内是毫秒级，足够用。
package diff

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Op 表示一行在 diff 中的角色。
type Op int

const (
	OpEqual  Op = iota // 两边都有
	OpDelete           // 仅左侧有（删除 / 左侧独有）
	OpInsert           // 仅右侧有（新增 / 右侧独有）
)

// String 用于调试 / JSON marshal。
func (o Op) String() string {
	switch o {
	case OpEqual:
		return "equal"
	case OpDelete:
		return "delete"
	case OpInsert:
		return "insert"
	default:
		return "unknown"
	}
}

// MarshalJSON 让 Op 在 JSON 里序列化为小写字符串，而不是 int。
//
// 不实现的话前端拿到的是 0/1/2，难以读懂也容易写错。
func (o Op) MarshalJSON() ([]byte, error) {
	return []byte(`"` + o.String() + `"`), nil
}

// UnmarshalJSON complements MarshalJSON for API/client round trips and tests.
func (o *Op) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch value {
	case "equal":
		*o = OpEqual
	case "delete":
		*o = OpDelete
	case "insert":
		*o = OpInsert
	default:
		return fmt.Errorf("unknown diff op %q", value)
	}
	return nil
}

// Line 是 diff 的最小展示单位。
//
//   - LeftNo / RightNo: 行号（1-based）。Op=Insert 时 LeftNo=0；Op=Delete 时 RightNo=0。
//   - Text: 已规范化后的行内容（不含行尾 \n）。
type Line struct {
	Op      Op     `json:"op"`
	LeftNo  int    `json:"left_no"`
	RightNo int    `json:"right_no"`
	Text    string `json:"text"`
}

// Stats 是给 UI 顶部的统计条用。
type Stats struct {
	LeftLines  int `json:"left_lines"`
	RightLines int `json:"right_lines"`
	Added      int `json:"added"`   // 仅右侧有
	Removed    int `json:"removed"` // 仅左侧有
	Common     int `json:"common"`
}

// Result 是 Compare 的返回。
type Result struct {
	Stats       Stats  `json:"stats"`
	Lines       []Line `json:"lines"`
	UnifiedDiff string `json:"unified_diff"`
}

// hunk 是 unified diff 输出时的一个变更块。包内使用，不导出。
type hunk struct {
	lStart, lCount, rStart, rCount int
	body                           []string // 已经带 +/-/空格 前缀的行
}

// Compare 对 left / right 做行级 Myers diff。
func Compare(left, right []string, leftLabel, rightLabel string) *Result {
	leftNos := make([]int, len(left))
	rightNos := make([]int, len(right))
	for i := range leftNos {
		leftNos[i] = i + 1
	}
	for i := range rightNos {
		rightNos[i] = i + 1
	}
	return CompareWithKeys(left, right, left, right, leftNos, rightNos, len(left), len(right), leftLabel, rightLabel)
}

// CompareWithKeys 用 keys 判断两行是否相等，但始终用 originals 生成结果。
// handler 可借此实现“忽略大小写/空白但保留原文”，lineNos 则让过滤空行后
// 的结果仍指向用户看到的真实行号。
func CompareWithKeys(leftOriginal, rightOriginal, leftKeys, rightKeys []string, leftNos, rightNos []int, leftTotal, rightTotal int, leftLabel, rightLabel string) *Result {
	if len(leftOriginal) != len(leftKeys) || len(rightOriginal) != len(rightKeys) ||
		len(leftOriginal) != len(leftNos) || len(rightOriginal) != len(rightNos) {
		panic("diff: originals, keys and line numbers must have equal lengths")
	}

	ops, truncated := myers(leftKeys, rightKeys)
	lines := make([]Line, 0, len(ops))
	for _, item := range ops {
		switch item.op {
		case OpEqual:
			lines = append(lines, Line{Op: OpEqual, LeftNo: leftNos[item.left], RightNo: rightNos[item.right], Text: leftOriginal[item.left]})
		case OpDelete:
			lines = append(lines, Line{Op: OpDelete, LeftNo: leftNos[item.left], Text: leftOriginal[item.left]})
		case OpInsert:
			lines = append(lines, Line{Op: OpInsert, RightNo: rightNos[item.right], Text: rightOriginal[item.right]})
		}
	}
	return buildResultWithTotals(lines, leftTotal, rightTotal, leftLabel, rightLabel, truncated)
}

type edit struct {
	op          Op
	left, right int
}

// maxMyersDistance 限制最坏情况下的 trace 内存。相似的大文件通常 D 很小，
// 能完整算出；完全不同的大文件超过阈值时安全退化为整体替换。
const maxMyersDistance = 2000

func myers(left, right []string) ([]edit, bool) {
	n, m := len(left), len(right)
	max := n + m
	if max == 0 {
		return nil, false
	}
	offset := max + 1
	v := make([]int, 2*max+3)
	for i := range v {
		v[i] = -1
	}
	v[offset+1] = 0
	trace := make([][]int, 0, minInt(max, maxMyersDistance)+1)

	for d := 0; d <= max && d <= maxMyersDistance; d++ {
		snapshot := append([]int(nil), v[offset-d-1:offset+d+2]...)
		trace = append(trace, snapshot)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && left[x] == right[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				return backtrack(trace, left, right, d), false
			}
		}
	}

	// 编辑距离过大：保留共同前后缀，中间安全地整体替换。
	prefix := 0
	for prefix < n && prefix < m && left[prefix] == right[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < n-prefix && suffix < m-prefix && left[n-1-suffix] == right[m-1-suffix] {
		suffix++
	}
	result := make([]edit, 0, n+m)
	for i := 0; i < prefix; i++ {
		result = append(result, edit{op: OpEqual, left: i, right: i})
	}
	for i := prefix; i < n-suffix; i++ {
		result = append(result, edit{op: OpDelete, left: i, right: -1})
	}
	for j := prefix; j < m-suffix; j++ {
		result = append(result, edit{op: OpInsert, left: -1, right: j})
	}
	for i := 0; i < suffix; i++ {
		result = append(result, edit{op: OpEqual, left: n - suffix + i, right: m - suffix + i})
	}
	return result, true
}

func backtrack(trace [][]int, left, right []string, distance int) []edit {
	x, y := len(left), len(right)
	reversed := make([]edit, 0, x+y)
	for d := distance; d > 0; d-- {
		k := x - y
		prev := trace[d]
		get := func(targetK int) int { return prev[targetK+d+1] }
		var prevK int
		if k == -d || (k != d && get(k-1) < get(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := get(prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			reversed = append(reversed, edit{op: OpEqual, left: x, right: y})
		}
		if x == prevX {
			y--
			reversed = append(reversed, edit{op: OpInsert, left: -1, right: y})
		} else {
			x--
			reversed = append(reversed, edit{op: OpDelete, left: x, right: -1})
		}
	}
	for x > 0 && y > 0 {
		x--
		y--
		reversed = append(reversed, edit{op: OpEqual, left: x, right: y})
	}
	for x > 0 {
		x--
		reversed = append(reversed, edit{op: OpDelete, left: x, right: -1})
	}
	for y > 0 {
		y--
		reversed = append(reversed, edit{op: OpInsert, left: -1, right: y})
	}
	for l, r := 0, len(reversed)-1; l < r; l, r = l+1, r-1 {
		reversed[l], reversed[r] = reversed[r], reversed[l]
	}
	return reversed
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildResult(lines []Line, left, right []string, leftLabel, rightLabel string, truncated bool) *Result {
	return buildResultWithTotals(lines, len(left), len(right), leftLabel, rightLabel, truncated)
}

func buildResultWithTotals(lines []Line, leftTotal, rightTotal int, leftLabel, rightLabel string, truncated bool) *Result {
	stats := Stats{
		LeftLines:  leftTotal,
		RightLines: rightTotal,
	}
	for _, l := range lines {
		switch l.Op {
		case OpEqual:
			stats.Common++
		case OpInsert:
			stats.Added++
		case OpDelete:
			stats.Removed++
		}
	}
	unified := renderUnified(lines, leftLabel, rightLabel, truncated)
	return &Result{
		Stats:       stats,
		Lines:       lines,
		UnifiedDiff: unified,
	}
}

// renderUnified 生成标准 unified diff 格式。
//
// 格式：
//
//	--- leftLabel
//	+++ rightLabel
//	@@ -l,s +r,s @@
//	 context
//	-deleted
//	+inserted
//
// truncated=true 时附加一行注释提示已退化为整体替换。
func renderUnified(lines []Line, leftLabel, rightLabel string, truncated bool) string {
	var b strings.Builder
	if leftLabel == "" {
		leftLabel = "left"
	}
	if rightLabel == "" {
		rightLabel = "right"
	}
	b.WriteString("--- ")
	b.WriteString(leftLabel)
	b.WriteByte('\n')
	b.WriteString("+++ ")
	b.WriteString(rightLabel)
	b.WriteByte('\n')

	// 收集 hunk：连续的 (OpEqual + OpDelete + OpInsert) 序列。
	// 每个 hunk 输出 @@ -lstart,lcount +rstart,rcount @@ + 行。
	const ctxLines = 3 // 上下文行数（标准是 3）

	var hunks []hunk
	cur := hunk{}
	curOpen := false
	closeHunk := func() {
		if curOpen {
			hunks = append(hunks, cur)
			cur = hunk{}
			curOpen = false
		}
	}

	leftLineNo := 0
	rightLineNo := 0
	// 上一次 hunk 结束的位置：用于决定下一个差异行是否要起新 hunk（看上下文间隔）。
	lastDiffIdx := -1

	for idx, ln := range lines {
		switch ln.Op {
		case OpEqual:
			leftLineNo++
			rightLineNo++
			if !curOpen {
				continue
			}
			// 在 hunk 内：作为上下文。如果当前 hunk 已经累计了变更，且距离上次差异行
			// 超过 2*ctxLines，就先关 hunk，等下一个差异行再开新的（带前导上下文）。
			if lastDiffIdx >= 0 && idx-lastDiffIdx > 2*ctxLines {
				closeHunk()
				continue
			}
			cur.lCount++
			cur.rCount++
			cur.body = append(cur.body, " "+ln.Text)
		case OpDelete:
			leftLineNo++
			if !curOpen {
				// 起新 hunk，带前导 ctxLines 上下文。
				openWithContext(lines, idx, ctxLines, &cur)
				curOpen = true
			}
			cur.lCount++
			cur.body = append(cur.body, "-"+ln.Text)
			lastDiffIdx = idx
		case OpInsert:
			rightLineNo++
			if !curOpen {
				openWithContext(lines, idx, ctxLines, &cur)
				curOpen = true
			}
			cur.rCount++
			cur.body = append(cur.body, "+"+ln.Text)
			lastDiffIdx = idx
		}
	}
	closeHunk()

	for _, h := range hunks {
		// lStart/rStart 已经在 openWithContext 中设置为 hunk 第一行（含上下文）的行号。
		lStart := h.lStart
		if lStart < 1 {
			lStart = 1
		}
		rStart := h.rStart
		if rStart < 1 {
			rStart = 1
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", lStart, h.lCount, rStart, h.rCount)
		for _, body := range h.body {
			b.WriteString(body)
			b.WriteByte('\n')
		}
	}

	if truncated {
		b.WriteString("\n# 注：两侧行数过多，已退化为整体替换视图，未生成逐行 unified diff。\n")
		b.WriteString("# 前端仍在 lines 字段里给出了完整 left+right 行供 side-by-side 渲染。\n")
	}
	return b.String()
}

// openWithContext 在位置 changeIdx 之前寻找最多 ctxLines 个 OpEqual 行作为上下文，
// 把它们以及后续内容塞进 hunk（局部类型，定义在 renderUnified 里）。
//
// hunk 的 lStart/rStart 设为第一个上下文 equal 的左/右行号（如果存在）；
// 否则设为第一个差异行的左/右行号。
func openWithContext(lines []Line, changeIdx, ctxLines int, h *hunk) {
	start := changeIdx - ctxLines
	if start < 0 {
		start = 0
	}
	// 从 start 向 changeIdx 走，把 equal 行作为上下文；遇到第一个非 equal 就停，
	// 因为 openWithContext 只在遇到第一个差异行时被调用，所以这里第一行就是 equal 或差异。
	for i := start; i < changeIdx; i++ {
		if lines[i].Op != OpEqual {
			start = i
			break
		}
	}
	for i := start; i < changeIdx; i++ {
		ln := lines[i]
		// 一定是 OpEqual（第一个非 equal 被跳过了）
		h.lCount++
		h.rCount++
		h.body = append(h.body, " "+ln.Text)
		if h.lStart == 0 && ln.LeftNo > 0 {
			h.lStart = ln.LeftNo
		}
		if h.rStart == 0 && ln.RightNo > 0 {
			h.rStart = ln.RightNo
		}
	}
	// lStart/rStart 兜底：万一没有上下文（changeIdx==0 或前面全是差异），设成 change 行本身的行号
	if h.lStart == 0 {
		// 找下一个 Delete 或 Insert 的行号
		for i := changeIdx; i < len(lines); i++ {
			if lines[i].Op == OpDelete && lines[i].LeftNo > 0 {
				h.lStart = lines[i].LeftNo
				break
			}
			if lines[i].Op == OpInsert && lines[i].RightNo > 0 {
				h.rStart = lines[i].RightNo
				break
			}
		}
	}
	if h.rStart == 0 {
		for i := changeIdx; i < len(lines); i++ {
			if lines[i].Op == OpInsert && lines[i].RightNo > 0 {
				h.rStart = lines[i].RightNo
				break
			}
		}
	}
}
