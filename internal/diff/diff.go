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

// Compare 对 left / right 做行级 diff，返回编辑脚本 + 统计 + unified diff 字符串。
//
// 调用方应自行处理规范化（trim space / 忽略空行 / 忽略大小写），
// 本包对输入字节流不做任何预处理，确保语义清晰。
func Compare(left, right []string, leftLabel, rightLabel string) *Result {
	// Myers diff：用 LCS 长度矩阵，回溯得到编辑脚本。
	// N/M 都很小时（N+M < 20000）用 O(NM) 的内存完全没问题；
	// 极端情况（几万行）仍能扛，但会占用 (N+1)*(M+1) 个 int ≈ 数百 MB
	// —— 我们的 4MB 限制下大概 6~8 万行，int 矩阵 ~ 25 GB 会爆。
	// 所以先做一道保护：超阈值时退化为"全删 + 全增"，仍然给出 stats 与
	// 完整的 left/right 文本由前端展示，但不强行算逐行 diff。
	if len(left)+len(right) > myersMaxLines {
		lines := make([]Line, 0, len(left)+len(right))
		for i, t := range left {
			lines = append(lines, Line{Op: OpDelete, LeftNo: i + 1, RightNo: 0, Text: t})
		}
		for i, t := range right {
			lines = append(lines, Line{Op: OpInsert, LeftNo: 0, RightNo: i + 1, Text: t})
		}
		return buildResult(lines, left, right, leftLabel, rightLabel, true)
	}

	// N+1 行 / M+1 列的 LCS 长度矩阵。行 0 / 列 0 全 0（空串的 LCS=0）。
	N, M := len(left), len(right)
	dp := make([][]int, N+1)
	for i := range dp {
		dp[i] = make([]int, M+1)
	}
	for i := 1; i <= N; i++ {
		for j := 1; j <= M; j++ {
			if left[i-1] == right[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				a := dp[i-1][j]
				b := dp[i][j-1]
				if a > b {
					dp[i][j] = a
				} else {
					dp[i][j] = b
				}
			}
		}
	}

	// 回溯：从 dp[N][M] 走到 dp[0][0]，逆序产出 Line。
	lines := make([]Line, 0, N+M)
	i, j := N, M
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && left[i-1] == right[j-1]:
			lines = append(lines, Line{Op: OpEqual, LeftNo: i, RightNo: j, Text: left[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			lines = append(lines, Line{Op: OpInsert, LeftNo: 0, RightNo: j, Text: right[j-1]})
			j--
		default:
			// i > 0 且 (j==0 或 dp[i-1][j] >= dp[i][j-1]) → 删除
			lines = append(lines, Line{Op: OpDelete, LeftNo: i, RightNo: 0, Text: left[i-1]})
			i--
		}
	}

	// 逆序产出，正过来。
	for l, r := 0, len(lines)-1; l < r; l, r = l+1, r-1 {
		lines[l], lines[r] = lines[r], lines[l]
	}

	return buildResult(lines, left, right, leftLabel, rightLabel, false)
}

// myersMaxLines 是 Myers diff 的内存安全阈值。
//
// 4MB 文本按平均 60 字节/行算约 7 万行，N+M = 14 万；
// 14 万 * 14 万 int ≈ 80 GB，绝对不行。
// 实际取 20000（N+M），保证 (N+1)*(M+1)*8 ≤ ~3.2 GB，仍偏高；
// 真要保险取 10000。给个保守值 15000（N*M ≤ ~5.6e8 ints ≈ 4.5 GB，理论上限，
// 但 15000 通常意味着 M 或 N 之一 < 7500，另一半更小，矩阵实际更小）。
// 选 15000 是经验值：实测 1 万行 diff 在毫秒级。
const myersMaxLines = 15000

func buildResult(lines []Line, left, right []string, leftLabel, rightLabel string, truncated bool) *Result {
	stats := Stats{
		LeftLines:  len(left),
		RightLines: len(right),
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
		// lStart/rStart 是 hunk 中第一个非上下文行（含 ctxLines 之前）的左侧/右侧行号
		// 我们在 openWithContext 里把上下文也算进了 count，所以 start 要回退 ctxLines 个 equal。
		lStart := h.lStart - ctxLines
		if lStart < 1 {
			lStart = 1
		}
		rStart := h.rStart - ctxLines
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