package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"kairo/internal/diff"
)

// compareReadLimit is the per-text payload cap shared by file reads and the
// in-memory diff endpoint. JSON escaping can expand one decoded byte to six
// bytes (for example, NUL as "\\u0000"). Keep the envelope bound derived from
// the two text fields so the documented 8 MiB-per-side contract also holds for
// worst-case escaped JSON; decoded fields are still checked against
// compareReadLimit below.
const (
	compareJSONEscapeMaxBytes = 6
	compareJSONEnvelopeBytes  = 256 * 1024
	compareJSONBodyLimit      = 2*compareReadLimit*compareJSONEscapeMaxBytes + compareJSONEnvelopeBytes
)

func decodeCompareJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	if limit <= 0 {
		limit = compareJSONBodyLimit
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(dst)
	if err == nil {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Errorf("请求体超过允许大小（上限 %d 字节）", limit))
	} else {
		writeErr(w, http.StatusBadRequest, err)
	}
	return false
}

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
	if !decodeCompareJSON(w, r, compareJSONBodyLimit, &req) {
		return
	}
	if int64(len([]byte(req.Left))) > compareReadLimit || int64(len([]byte(req.Right))) > compareReadLimit {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Errorf("单侧文本超过 %d 字节上限", compareReadLimit))
		return
	}

	// Bound the per-line slices before splitLines allocates them. A small
	// byte payload containing millions of newlines is still expensive.
	if strings.Count(req.Left, "\n") >= 200000 || strings.Count(req.Right, "\n") >= 200000 {
		writeErr(w, http.StatusRequestEntityTooLarge, errors.New("text exceeds 200000 lines per side"))
		return
	}
	leftLines := splitLines(req.Left)
	rightLines := splitLines(req.Right)
	leftOriginal, leftKeys, leftNos := prepareLines(leftLines, req.Ignore)
	rightOriginal, rightKeys, rightNos := prepareLines(rightLines, req.Ignore)

	res := diff.CompareWithKeys(
		leftOriginal, rightOriginal,
		leftKeys, rightKeys,
		leftNos, rightNos,
		len(leftLines), len(rightLines),
		req.LeftLabel, req.RightLabel,
	)

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

// prepareLines 同时保留原文和用于匹配的 key，避免忽略规则污染展示和下载结果。
//
// 规则：
//   - TrimSpace：去掉行尾的空白字符（tab / space），不动行首；
//     这样"  a"和"a"会判等，"a  "和"a"也会判等，但"  a"和"a  "还会判等。
//     （保持行内字符相对位置，仅消除右端 padding，便于纯文本比对）
//   - IgnoreBlank：从参与比对的行中移除空白行，同时保留真实行号。
//   - IgnoreCase：转小写。
//
// 这三个规则的设计是：先 TrimSpace，再判 IgnoreBlank（此时多数空行已归一），
// 最后 IgnoreCase。TrimSpace 用 strings.TrimRight(line, " \t")。
func prepareLines(lines []string, ig compareIgnore) (originals, keys []string, lineNos []int) {
	originals = make([]string, 0, len(lines))
	keys = make([]string, 0, len(lines))
	lineNos = make([]int, 0, len(lines))
	for i, line := range lines {
		l := line
		if ig.TrimSpace {
			l = strings.TrimRight(l, " \t")
		}
		if ig.IgnoreBlank && strings.TrimSpace(l) == "" {
			continue
		}
		if ig.IgnoreCase {
			l = strings.ToLower(l)
		}
		originals = append(originals, line)
		keys = append(keys, l)
		lineNos = append(lineNos, i+1)
	}
	return originals, keys, lineNos
}
