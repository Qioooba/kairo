package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"ops-toolbox/internal/diff"
)

// compareReq 是 /api/diff/compare 的请求体。
//
//   - Left / Right：两侧文本（必填）。label 用于 unified diff 的文件头展示，可选。
//   - Ignore：忽略规则。可选，默认 trim_space=true、ignore_blank=false、ignore_case=false。
type compareReq struct {
	Left       string        `json:"left"`
	Right      string        `json:"right"`
	LeftLabel  string        `json:"left_label"`
	RightLabel string        `json:"right_label"`
	Ignore     compareIgnore `json:"ignore"`
}

type compareIgnore struct {
	TrimSpace   bool `json:"trim_space"`
	IgnoreBlank bool `json:"ignore_blank"`
	IgnoreCase  bool `json:"ignore_case"`
}

// diffCompareResp 是 /api/diff/compare 的响应。
type diffCompareResp struct {
	OK          bool        `json:"ok"`
	Stats       diff.Stats  `json:"stats"`
	Lines       []diff.Line `json:"lines"`
	UnifiedDiff string      `json:"unified_diff"`
}

func (s *Server) handleDiffCompare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req compareReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}

	leftLines := splitLines(req.Left)
	rightLines := splitLines(req.Right)

	leftLines = applyIgnore(leftLines, req.Ignore)
	rightLines = applyIgnore(rightLines, req.Ignore)

	res := diff.Compare(leftLines, rightLines, req.LeftLabel, req.RightLabel)

	writeJSON(w, 200, diffCompareResp{
		OK:          true,
		Stats:       res.Stats,
		Lines:       res.Lines,
		UnifiedDiff: res.UnifiedDiff,
	})
}

// splitLines 把字符串按 \n 拆成行，去掉每个行尾的 \r（兼容 CRLF）。
//
// 末尾的空行（input 以 \n 结尾产生的那种）会保留，因为我们以行为单位比对，
// 不希望"a\nb\n"和"a\nb"产生差异。
func splitLines(s string) []string {
	if s == "" {
		return []string{}
	}
	// 拆分前先把 \r\n 统一为 \n，再 split。
	// 注意：strings.Split(s, "\n") 会保留末尾的空字符串；如果原串以 \n 结尾，
	// 我们想表达"最后是空行"，所以保留；否则也不该凭空多一行。
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, len(raw))
	for i, line := range raw {
		out[i] = line
	}
	return out
}

// applyIgnore 在拆行后对每行做 ignore 规则处理。
//
// 规则：
//   - TrimSpace：去掉行尾的空白字符（tab / space），不动行首；
//     这样"  a"和"a"会判等，"a  "和"a"也会判等，但"  a"和"a  "还会判等。
//     （保持行内字符相对位置，仅消除右端 padding，便于纯文本比对）
//   - IgnoreBlank：把"trim 后为空"的行替换成空字符串（已经空了）；
//     这条生效后，前后两份文本的空行数量必须一致才不会产生"伪差异"。
//     我们的语义：两边都丢掉空行后比对，等价于先压缩空白行再比对。
//   - IgnoreCase：转小写。
//
// 这三个规则的设计是：先 TrimSpace，再判 IgnoreBlank（此时多数空行已归一），
// 最后 IgnoreCase。TrimSpace 用 strings.TrimRight(line, " \t")。
func applyIgnore(lines []string, ig compareIgnore) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		l := line
		if ig.TrimSpace {
			l = strings.TrimRight(l, " \t")
		}
		if ig.IgnoreBlank && strings.TrimSpace(l) == "" {
			l = ""
		}
		if ig.IgnoreCase {
			l = strings.ToLower(l)
		}
		out[i] = l
	}
	return out
}
