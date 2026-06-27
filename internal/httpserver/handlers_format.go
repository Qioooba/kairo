package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ops-toolbox/internal/formatter"
)

type formatJSONReq struct {
	Input  string `json:"input"`
	Mode   string `json:"mode"`   // format | minify | validate
	Indent string `json:"indent"` // 缩进字符串，默认 "  "
}

func (s *Server) handleFormatJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatJSONReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "format"
	}
	indent := req.Indent
	if indent == "" {
		indent = "  "
	}
	var out string
	var err error
	switch mode {
	case "format":
		out, err = formatter.FormatJSON(req.Input, indent)
	case "minify":
		out, err = formatter.MinifyJSON(req.Input)
	case "validate":
		if err = formatter.ValidateJSON(req.Input); err == nil {
			writeJSON(w, 200, map[string]any{"ok": true})
			return
		}
	default:
		writeErr(w, 400, errors.New("mode 仅支持 format/minify/validate"))
		return
	}
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": out})
}

// ---------- /api/format/xml ----------

type formatXMLReq struct {
	Input  string `json:"input"`
	Mode   string `json:"mode"`
	Indent string `json:"indent"`
}

func (s *Server) handleFormatXML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatXMLReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "format"
	}
	indent := req.Indent
	if indent == "" {
		indent = "  "
	}
	var out string
	var err error
	switch mode {
	case "format":
		out, err = formatter.FormatXML(req.Input, indent)
	case "minify":
		out, err = formatter.MinifyXML(req.Input)
	default:
		writeErr(w, 400, errors.New("mode 仅支持 format/minify"))
		return
	}
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": out})
}

// ---------- /api/format/yaml ----------
//
// 支持模式：
//   - format：YAML 格式化（默认 indent=2）
//   - minify：单行紧凑
//   - validate：仅校验合法性
//   - to_json：YAML → JSON
//   - from_json：JSON → YAML
//
// 入参额外字段：
//   - indent：format / to_json / from_json 时控制缩进；
//     format / from_json 是 YAML 缩进空格数（默认 2，clamp 到 [1,8]）；
//     to_json 是 JSON 缩进字符串（默认 "  " 表示 2 空格）

type formatYAMLReq struct {
	Input  string `json:"input"`
	Mode   string `json:"mode"`   // format | minify | validate | to_json | from_json
	Indent string `json:"indent"` // 字符串，按 mode 解释
}

func (s *Server) handleFormatYAML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatYAMLReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "format"
	}

	switch mode {
	case "format":
		indent, _ := strconv.Atoi(strings.TrimSpace(req.Indent))
		if indent <= 0 {
			indent = 2
		}
		out, err := formatter.FormatYAML(req.Input, indent)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "output": out})
	case "minify":
		out, err := formatter.MinifyYAML(req.Input)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "output": out})
	case "validate":
		if err := formatter.ValidateYAML(req.Input); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	case "to_json":
		indent := req.Indent
		if indent == "" {
			indent = "  "
		}
		out, err := formatter.YAMLToJSON(req.Input, indent)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "output": out})
	case "from_json":
		indent, _ := strconv.Atoi(strings.TrimSpace(req.Indent))
		if indent <= 0 {
			indent = 2
		}
		out, err := formatter.JSONToYAML(req.Input, indent)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "output": out})
	default:
		writeErr(w, 400, errors.New("mode 仅支持 format/minify/validate/to_json/from_json"))
	}
}

// ---------- /api/format/url-form ----------
//
// 模式：
//   - encode：map → "a=1&b=2"
//   - decode："a=1&b=2" → map（同名 key 自动合并成数组）
//
// 入参：
//   - encode 模式：input 是 JSON 对象字符串（前端解析后转 map；这里直接接受任意
//     key-value 输入比较麻烦，所以约定 input 是 JSON，handler 用 json.Unmarshal 解
//     到 map[string]string）
//   - decode 模式：input 是 raw query string

type formatURLFormReq struct {
	Input string `json:"input"`
	Mode  string `json:"mode"` // encode | decode
}

func (s *Server) handleFormatURLForm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatURLFormReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	switch mode {
	case "encode":
		// input 约定是 JSON 对象字符串，前端用 JSON.stringify(map) 即可
		raw := strings.TrimSpace(req.Input)
		if raw == "" {
			writeJSON(w, 200, map[string]any{"ok": true, "output": ""})
			return
		}
		var kv map[string]string
		if err := json.Unmarshal([]byte(raw), &kv); err != nil {
			writeErr(w, 400, fmt.Errorf("encode 模式 input 必须是 JSON 对象字符串：%w", err))
			return
		}
		out, err := formatter.URLFormEncode(kv)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "output": out})
	case "decode":
		out, err := formatter.URLFormDecode(req.Input)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		// 转回 JSON 给前端
		b, _ := json.MarshalIndent(out, "", "  ")
		writeJSON(w, 200, map[string]any{"ok": true, "output": string(b), "kv": out})
	default:
		writeErr(w, 400, errors.New("mode 仅支持 encode/decode"))
	}
}

// ---------- /api/format/timestamp ----------

type formatTimestampReq struct {
	Input  string `json:"input"`
	FromTZ string `json:"from_tz"`
	ToTZ   string `json:"to_tz"`
}

func (s *Server) handleFormatTimestamp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatTimestampReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	fromLoc, err := parseLocation(req.FromTZ)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("from_tz 无效: %w", err))
		return
	}
	toLoc, err := parseLocation(req.ToTZ)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("to_tz 无效: %w", err))
		return
	}
	t, inputType, err := parseTimestampInput(req.Input, fromLoc)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": true, "res": map[string]any{
			"input_type": "invalid",
			"error":      err.Error(),
		}})
		return
	}
	target := t.In(toLoc)
	writeJSON(w, 200, map[string]any{"ok": true, "res": map[string]any{
		"input_type": inputType,
		"utc":        t.UTC().Format(time.RFC3339),
		"local":      t.In(time.Local).Format("2006-01-02 15:04:05 MST"),
		"target":     target.Format("2006-01-02 15:04:05 MST"),
		"unix_sec":   t.Unix(),
		"unix_milli": t.UnixMilli(),
		"year":       target.Year(),
		"month":      int(target.Month()),
		"day":        target.Day(),
		"hour":       target.Hour(),
		"minute":     target.Minute(),
		"second":     target.Second(),
		"weekday_cn": weekdayCN(target.Weekday()),
	}})
}

func parseLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "Local") {
		return time.Local, nil
	}
	if strings.EqualFold(name, "UTC") {
		return time.UTC, nil
	}
	return time.LoadLocation(name)
}

func parseTimestampInput(input string, loc *time.Location) (time.Time, string, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return time.Time{}, "", errors.New("输入不能为空")
	}
	if strings.EqualFold(raw, "now") {
		return time.Now(), "now", nil
	}
	if isDigits(raw) {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return time.Time{}, "", fmt.Errorf("数字时间戳解析失败: %w", err)
		}
		switch {
		case len(raw) >= 13:
			return time.UnixMilli(n), "unix_milli", nil
		default:
			return time.Unix(n, 0), "unix_sec", nil
		}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, "iso8601", nil
		}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"2006-01-02",
		"2006/01/02",
		"01-02-2006 15:04:05",
		"01-02-2006",
	} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t, "human", nil
		}
	}
	return time.Time{}, "", errors.New("无法识别时间格式")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func weekdayCN(w time.Weekday) string {
	return []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}[int(w)]
}

// ---------- /api/format/jsonpath ----------

type formatJSONPathReq struct {
	Input  string `json:"input"`
	Path   string `json:"path"`
	Strict bool   `json:"strict"`
}

func (s *Server) handleFormatJSONPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatJSONPathReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	start := time.Now()
	path := strings.TrimSpace(req.Path)
	if path == "" {
		writeErr(w, 400, errors.New("path 不能为空"))
		return
	}
	var data any
	dec := json.NewDecoder(strings.NewReader(req.Input))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	if req.Strict {
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			writeErr(w, 400, errors.New("JSON 后面存在多余内容"))
			return
		}
	}
	values, found := evalSimpleJSONPath(data, path)
	if !found {
		writeJSON(w, 200, map[string]any{"ok": true, "found": false, "result": "", "type": "missing", "elapsed_ms": time.Since(start).Milliseconds()})
		return
	}
	var out any = values[0]
	isArray := len(values) > 1
	if isArray {
		out = values
	}
	result, typ := jsonPathResultString(out)
	writeJSON(w, 200, map[string]any{
		"ok":         true,
		"found":      true,
		"result":     result,
		"type":       typ,
		"is_array":   isArray,
		"elapsed_ms": time.Since(start).Milliseconds(),
	})
}

func evalSimpleJSONPath(root any, path string) ([]any, bool) {
	tokens := strings.Split(strings.Trim(path, "."), ".")
	cur := []any{root}
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		next := make([]any, 0)
		for _, node := range cur {
			switch n := node.(type) {
			case map[string]any:
				v, ok := n[tok]
				if ok {
					next = append(next, v)
				}
			case []any:
				if tok == "#" || tok == "*" {
					next = append(next, n...)
					continue
				}
				if idx, err := strconv.Atoi(tok); err == nil && idx >= 0 && idx < len(n) {
					next = append(next, n[idx])
				}
			}
		}
		if len(next) == 0 {
			return nil, false
		}
		cur = next
	}
	return cur, len(cur) > 0
}

func jsonPathResultString(v any) (string, string) {
	switch x := v.(type) {
	case nil:
		return "null", "null"
	case string:
		return x, "string"
	case bool:
		return strconv.FormatBool(x), "bool"
	case json.Number:
		return x.String(), "number"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), "number"
	default:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
		typ := "object"
		if _, ok := v.([]any); ok {
			typ = "array"
		}
		return strings.TrimRight(buf.String(), "\n"), typ
	}
}

// ---------- /api/format/cron-parse ----------

type formatCronReq struct {
	Input string `json:"input"`
	Loc   string `json:"loc"`
}

func (s *Server) handleFormatCronParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatCronReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	loc, err := parseLocation(req.Loc)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("loc 无效: %w", err))
		return
	}
	res := parseCronPreview(strings.TrimSpace(req.Input), loc)
	writeJSON(w, 200, map[string]any{"ok": true, "res": res})
}

func parseCronPreview(input string, loc *time.Location) map[string]any {
	out := map[string]any{
		"valid":       false,
		"has_seconds": false,
		"field_desc":  "",
		"next_runs":   []any{},
		"prev_runs":   []any{},
	}
	if input == "" {
		out["error"] = "表达式不能为空"
		return out
	}
	if strings.HasPrefix(input, "@every ") {
		d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(input, "@every ")))
		if err != nil || d <= 0 {
			out["error"] = "@every 后面需要合法 duration，如 5s / 1h30m"
			return out
		}
		now := time.Now().In(loc)
		next, prev := make([]map[string]any, 0, 5), make([]map[string]any, 0, 3)
		for i := 1; i <= 5; i++ {
			next = append(next, cronRun(now.Add(time.Duration(i)*d)))
		}
		for i := 1; i <= 3; i++ {
			prev = append(prev, cronRun(now.Add(-time.Duration(i)*d)))
		}
		out["valid"] = true
		out["has_seconds"] = d < time.Minute
		out["field_desc"] = "固定间隔执行: " + d.String()
		out["next_runs"] = next
		out["prev_runs"] = prev
		return out
	}
	spec := expandCronDescriptor(input)
	fields := strings.Fields(spec)
	if len(fields) != 5 && len(fields) != 6 {
		out["error"] = "Cron 需要 5 段或 6 段，或使用 @hourly / @daily / @every"
		return out
	}
	hasSeconds := len(fields) == 6
	offset := 0
	if !hasSeconds {
		fields = append([]string{"0"}, fields...)
		offset = 1
	}
	sets, err := parseCronFields(fields)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	now := time.Now().In(loc)
	next := collectCronRuns(now, 1, 5, sets, hasSeconds)
	prev := collectCronRuns(now, -1, 3, sets, hasSeconds)
	out["valid"] = true
	out["has_seconds"] = hasSeconds
	out["field_desc"] = cronFieldDesc(fields, offset)
	out["next_runs"] = next
	out["prev_runs"] = prev
	return out
}

func expandCronDescriptor(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "@yearly", "@annually":
		return "0 0 0 1 1 *"
	case "@monthly":
		return "0 0 0 1 * *"
	case "@weekly":
		return "0 0 0 * * 0"
	case "@daily", "@midnight":
		return "0 0 0 * * *"
	case "@hourly":
		return "0 0 * * * *"
	default:
		return input
	}
}

func parseCronFields(fields []string) ([]map[int]bool, error) {
	ranges := [][2]int{{0, 59}, {0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	sets := make([]map[int]bool, len(fields))
	for i, f := range fields {
		set, err := parseCronField(f, ranges[i][0], ranges[i][1])
		if err != nil {
			return nil, fmt.Errorf("第 %d 段解析失败: %w", i+1, err)
		}
		if i == 5 && set[7] {
			set[0] = true
			delete(set, 7)
		}
		sets[i] = set
	}
	return sets, nil
}

func parseCronField(expr string, min, max int) (map[int]bool, error) {
	out := make(map[int]bool)
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("空字段")
		}
		step := 1
		base := part
		if strings.Contains(part, "/") {
			pair := strings.SplitN(part, "/", 2)
			base = pair[0]
			n, err := strconv.Atoi(pair[1])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("非法步长 %q", pair[1])
			}
			step = n
		}
		start, end := min, max
		switch {
		case base == "*" || base == "?":
		case strings.Contains(base, "-"):
			pair := strings.SplitN(base, "-", 2)
			a, errA := strconv.Atoi(pair[0])
			b, errB := strconv.Atoi(pair[1])
			if errA != nil || errB != nil || a > b {
				return nil, fmt.Errorf("非法范围 %q", base)
			}
			start, end = a, b
		default:
			n, err := strconv.Atoi(base)
			if err != nil {
				return nil, fmt.Errorf("非法值 %q", base)
			}
			start, end = n, n
		}
		if start < min || end > max {
			return nil, fmt.Errorf("值超出范围 %d-%d", min, max)
		}
		for i := start; i <= end; i += step {
			out[i] = true
		}
	}
	return out, nil
}

func collectCronRuns(now time.Time, dir, want int, sets []map[int]bool, hasSeconds bool) []map[string]any {
	step := time.Minute
	if hasSeconds {
		step = time.Second
	}
	if dir > 0 {
		now = now.Truncate(step).Add(step)
	} else {
		now = now.Truncate(step).Add(-step)
		step = -step
	}
	out := make([]map[string]any, 0, want)
	t := now
	if dir > 0 {
		// BE-015: 正向用字段递进 + 跳跃算法，避免稀疏 cron（如 `0 0 0 1 1 *`
		// 每年1月1日，5 次≈262 万分钟）超过旧版 200 万次暴力迭代上限。
		// 每次跳跃直接到下一个可能匹配的时间点，最多几千次循环即可。
		const maxIter = 10000
		for i := 0; i < maxIter && len(out) < want; i++ {
			t = nextCronMatchForward(t, sets, hasSeconds)
			out = append(out, cronRun(t))
			t = t.Add(step)
		}
		return out
	}
	// 反向仍用暴力遍历（want 通常仅 3，迭代量小）。
	for i := 0; i < 2000000 && len(out) < want; i++ {
		if cronMatch(t, sets) {
			out = append(out, cronRun(t))
		}
		t = t.Add(step)
	}
	return out
}

// nextCronMatchForward 从 from（含）开始找下一个匹配 cron 表达式的时间点。
// BE-015：按 月 → 日 → 时 → 分 → 秒 顺序递进，任一字段不匹配即跳到下一个
// 该字段可能匹配的边界，避免逐分钟暴力遍历。
func nextCronMatchForward(from time.Time, sets []map[int]bool, hasSeconds bool) time.Time {
	loc := from.Location()
	t := from
	for i := 0; i < 100000; i++ {
		if !sets[4][int(t.Month())] {
			// 月份不匹配：跳到下个月 1 号 00:00:00（Go 自动归一化 12→次年1月）。
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !dayMatches(t, sets) {
			// 日期不匹配：跳到下一天 00:00:00。
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
			continue
		}
		if !sets[2][t.Hour()] {
			// 小时不匹配：跳到下一小时 00 分 00 秒。
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc).Add(time.Hour)
			continue
		}
		if !sets[1][t.Minute()] {
			// 分钟不匹配：跳到下一分钟 00 秒。
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc).Add(time.Minute)
			continue
		}
		if hasSeconds && !sets[0][t.Second()] {
			t = t.Add(time.Second)
			continue
		}
		return t
	}
	return from
}

// dayMatches 判断 t 的日期部分是否匹配 cron 的 day-of-month 和 day-of-week 字段。
// 与 cronMatch 保持一致的 AND 语义（既有行为，不改 cron 标准 OR 规则）。
func dayMatches(t time.Time, sets []map[int]bool) bool {
	return sets[3][t.Day()] && sets[5][int(t.Weekday())]
}

func cronMatch(t time.Time, sets []map[int]bool) bool {
	return sets[0][t.Second()] &&
		sets[1][t.Minute()] &&
		sets[2][t.Hour()] &&
		sets[3][t.Day()] &&
		sets[4][int(t.Month())] &&
		sets[5][int(t.Weekday())]
}

func cronRun(t time.Time) map[string]any {
	return map[string]any{
		"cn":   t.Format("2006-01-02 15:04:05 MST"),
		"unix": t.Unix(),
	}
}

func cronFieldDesc(fields []string, offset int) string {
	if offset == 1 {
		return fmt.Sprintf("分钟=%s，小时=%s，日=%s，月=%s，周=%s", fields[1], fields[2], fields[3], fields[4], fields[5])
	}
	return fmt.Sprintf("秒=%s，分钟=%s，小时=%s，日=%s，月=%s，周=%s", fields[0], fields[1], fields[2], fields[3], fields[4], fields[5])
}
