package httpserver

// ---------- /api/http/curl-parse ----------
//
// HTTP 测试页的「导入 cURL」：粘贴一段 cURL 命令，解析出
// method / url / headers / body（含 body_mode / body_type / body_form），
// 前端回填到请求编辑器。
//
// 解析规则（shell 风格分词）：
//   - 单引号 / 双引号包裹的段整体算一个 token（去掉外层引号，双引号内 \" 转义）
//   - 行尾 \ 续行会被拼回一行
//   - -X/--request → method；裸 http(s):// token → url
//   - -H/--header → header（按第一个冒号切开）；-A/-e/-b/-u 映射到对应 header
//   - -d/--data/--data-ascii/--data-urlencode → urlencoded body（多段用 & 连接）
//   - --data-raw/--data-binary/--json → raw body
//   - -F/--form → formdata 字段
//   - -k/--insecure → insecure_tls；-L/--location → follow_redirect；--max-redirs 0 → 不跟随
//   - -m/--max-time N → timeout_ms
//   - -I/--head → HEAD；-G/--get → GET

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type httpCurlParseResp struct {
	Ok             bool              `json:"ok"`
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers"`
	Body           string            `json:"body"`
	BodyMode       string            `json:"body_mode"` // none | formdata | urlencoded | raw
	BodyType       string            `json:"body_type"` // json | xml | html | text
	BodyForm       map[string]string `json:"body_form"`
	TimeoutMs      int               `json:"timeout_ms"`
	FollowRedirect bool              `json:"follow_redirect"`
	InsecureTLS    bool              `json:"insecure_tls"`
}

func (s *Server) handleHTTPCurlParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req struct {
		Curl string `json:"curl"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("JSON 解析失败: "+err.Error()))
		return
	}
	if strings.TrimSpace(req.Curl) == "" {
		writeErr(w, 400, errors.New("cURL 命令不能为空"))
		return
	}
	parsed, err := parseCurlCommand(req.Curl)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.audit.Write("http.curl.parse", "url", parsed.URL)
	writeJSON(w, 200, parsed)
}

// tokenizeCurl 把 cURL 命令切成 token：
//   - 处理行尾 \ 续行（把整条命令拼成一行再切）
//   - 单引号段：原样保留内容（不含引号）
//   - 双引号段：保留内容，处理 \" 与 \\
//   - 其余空白分隔；\x 转义在引号外保留 x
func tokenizeCurl(s string) []string {
	joined := strings.ReplaceAll(s, "\\\r\n", "")
	joined = strings.ReplaceAll(joined, "\\\n", "")
	var tokens []string
	var cur strings.Builder
	inSingle, inDouble, escaped := false, false, false
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, ch := range joined {
		switch {
		case escaped:
			cur.WriteRune(ch)
			escaped = false
		case inSingle:
			if ch == '\'' {
				inSingle = false
			} else {
				cur.WriteRune(ch)
			}
		case inDouble:
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inDouble = false
			default:
				cur.WriteRune(ch)
			}
		default:
			switch ch {
			case '\'':
				inSingle = true
			case '"':
				inDouble = true
			case '\\':
				escaped = true
			case ' ', '\t', '\r', '\n':
				flush()
			default:
				cur.WriteRune(ch)
			}
		}
	}
	flush()
	return tokens
}

func parseCurlCommand(s string) (httpCurlParseResp, error) {
	resp := httpCurlParseResp{
		Ok:             true,
		Method:         "GET",
		Headers:        map[string]string{},
		BodyMode:       "none",
		BodyType:       "text",
		FollowRedirect: true,
	}
	tokens := tokenizeCurl(s)
	if len(tokens) == 0 {
		return resp, errors.New("cURL 命令为空")
	}
	if !strings.HasPrefix(strings.ToLower(tokens[0]), "curl") {
		return resp, errors.New("不是 cURL 命令（应以 curl 开头）")
	}

	var dataParts []string
	var rawBody string
	rawBodySet := false
	formMode := false
	formMap := map[string]string{}
	maxTimeSec := 0

	need := func(i int, flag string) (string, error) {
		if i+1 >= len(tokens) {
			return "", errors.New(flag + " 缺少参数")
		}
		return tokens[i+1], nil
	}

	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		lower := strings.ToLower(t)
		switch {
		case t == "-X" || lower == "--request":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			resp.Method = strings.ToUpper(strings.TrimSpace(v))
			i++
		case strings.HasPrefix(t, "-X") && len(t) > 2:
			// -XPOST 连写
			resp.Method = strings.ToUpper(strings.TrimSpace(t[2:]))
		case t == "-H" || lower == "--header":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			addCurlHeader(resp.Headers, v)
			i++
		case t == "-A" || lower == "--user-agent":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			resp.Headers["User-Agent"] = v
			i++
		case t == "-e" || lower == "--referer":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			resp.Headers["Referer"] = v
			i++
		case t == "-b" || lower == "--cookie":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			resp.Headers["Cookie"] = v
			i++
		case t == "-u" || lower == "--user":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			resp.Headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(v))
			i++
		case t == "-d" || lower == "--data" || lower == "--data-ascii" || lower == "--data-urlencode":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			dataParts = append(dataParts, v)
			i++
		case lower == "--data-raw" || lower == "--data-binary":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			rawBody = v
			rawBodySet = true
			i++
		case lower == "--json":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			rawBody = v
			rawBodySet = true
			if _, ok := resp.Headers["Content-Type"]; !ok {
				resp.Headers["Content-Type"] = "application/json"
			}
			i++
		case t == "-F" || lower == "--form":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			formMode = true
			parseCurlFormField(v, formMap)
			i++
		case t == "-k" || lower == "--insecure":
			resp.InsecureTLS = true
		case t == "-L" || lower == "--location":
			resp.FollowRedirect = true
		case lower == "--max-redirs":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			if strings.TrimSpace(v) == "0" {
				resp.FollowRedirect = false
			}
			i++
		case t == "-m" || lower == "--max-time":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			if n, err2 := strconv.Atoi(strings.TrimSpace(v)); err2 == nil && n > 0 {
				maxTimeSec = n
			}
			i++
		case t == "-I" || lower == "--head":
			resp.Method = "HEAD"
		case t == "-G" || lower == "--get":
			resp.Method = "GET"
		case lower == "--url":
			v, err := need(i, t)
			if err != nil {
				return resp, err
			}
			resp.URL = v
			i++
		case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
			resp.URL = t
		case strings.HasPrefix(t, "-") || strings.HasPrefix(t, "--"):
			// 已知可忽略的 curl 旗标（-s -S -v --compressed -o file ...）
			// 带参旗标：若下一 token 不是旗标则跳过参数（-o out.txt 等）
			if isCurlFlagWithArg(t) && i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
				i++
			}
		default:
			// 无法识别的裸 token：忽略（多数情况是 curl 之外的东西）
		}
	}

	if resp.URL == "" {
		return resp, errors.New("未找到 URL（需要 http:// 或 https:// 地址）")
	}
	if resp.Method == "" {
		resp.Method = "GET"
	}
	if resp.Method != "GET" && resp.Method != "HEAD" {
		resp.Method = strings.ToUpper(resp.Method)
	}

	if maxTimeSec > 0 {
		resp.TimeoutMs = maxTimeSec * 1000
	}

	// body 决策
	switch {
	case formMode && !rawBodySet:
		resp.BodyMode = "formdata"
		resp.BodyForm = formMap
		for k, v := range formMap {
			resp.Body += url.QueryEscape(k) + "=" + url.QueryEscape(v) + "&"
		}
		resp.Body = strings.TrimSuffix(resp.Body, "&")
	case rawBodySet:
		resp.BodyMode = "raw"
		resp.Body = rawBody
		resp.BodyType = guessBodyType(rawBody)
	case len(dataParts) > 0:
		joined := strings.Join(dataParts, "&")
		if isURLEncodedBody(joined) {
			resp.BodyMode = "urlencoded"
			resp.Body = joined
			resp.BodyForm = parseURLEncoded(joined)
		} else {
			resp.BodyMode = "raw"
			resp.Body = joined
			resp.BodyType = guessBodyType(joined)
		}
	default:
		resp.BodyMode = "none"
	}

	return resp, nil
}

// addCurlHeader 解析 "Key: Value" 或 "Key;"（curl -H 特殊语法，空值）
func addCurlHeader(dst map[string]string, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	idx := strings.Index(raw, ":")
	if idx < 0 {
		// "Cookie;" / "X-Custom;" 这类 curl 空值语法
		dst[strings.TrimSuffix(raw, ";")] = ""
		return
	}
	k := strings.TrimSpace(raw[:idx])
	v := strings.TrimSpace(raw[idx+1:])
	if k == "" {
		return
	}
	dst[k] = v
}

// parseCurlFormField 解析 -F 'name=value' / -F 'name=@file' / -F 'name=value;type=...'
func parseCurlFormField(raw string, dst map[string]string) {
	idx := strings.Index(raw, "=")
	if idx < 0 {
		return
	}
	k := strings.TrimSpace(raw[:idx])
	v := strings.TrimSpace(raw[idx+1:])
	if k == "" {
		return
	}
	if strings.HasPrefix(v, "@") {
		dst[k] = "@" + strings.TrimPrefix(v, "@") // 文件上传：仅保留占位提示
		return
	}
	// 去掉 ;type=application/json 之类的参数
	if semi := strings.Index(v, ";"); semi >= 0 {
		v = v[:semi]
	}
	dst[k] = strings.Trim(v, `"`)
}

// isURLEncodedBody 判断 -d 的内容是不是 k=v&k2=v2 形态
func isURLEncodedBody(s string) bool {
	if s == "" || !strings.Contains(s, "=") {
		return false
	}
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return false
	}
	return true
}

func parseURLEncoded(s string) map[string]string {
	out := map[string]string{}
	for _, p := range strings.Split(s, "&") {
		idx := strings.Index(p, "=")
		if idx < 0 {
			continue
		}
		k, _ := url.QueryUnescape(p[:idx])
		v, _ := url.QueryUnescape(p[idx+1:])
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func guessBodyType(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "text"
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		if json.Valid([]byte(trimmed)) {
			return "json"
		}
		return "text"
	}
	if strings.HasPrefix(trimmed, "<") {
		return "xml"
	}
	return "text"
}

// isCurlFlagWithArg 这些旗标后跟一个值（-o out.txt、--output out.txt、-w fmt ...）。
// 短旗标区分大小写（-d data 有参；-D file 也有参；-F form 有参；-f fail 无参）。
func isCurlFlagWithArg(t string) bool {
	switch t {
	case "-o", "-w", "-d", "-T", "-D", "-c", "-C", "-n", "-x", "-U",
		"-r", "-y", "-Y", "-z", "-Q", "-E", "-t", "-P", "-B", "-F",
		"-H", "-e", "-b", "-u", "-A", "-m", "-M", "-K", "-j", "-J":
		return true
	}
	switch strings.ToLower(t) {
	case "--output", "--write-out", "--data", "--data-raw", "--data-ascii",
		"--data-binary", "--data-urlencode", "--json", "--upload-file",
		"--cacert", "--capath", "--cert", "--key", "--dump-header",
		"--cookie-jar", "--connect-timeout", "--retry", "--retry-delay",
		"--max-filesize", "--limit-rate", "--netrc", "--proxy",
		"--proxy-user", "--resolve", "--noproxy", "--range", "--socks5",
		"--trace", "--trace-ascii", "--tls-max", "--form-string",
		"--url", "--header", "--referer", "--cookie", "--user",
		"--user-agent", "--form", "--max-time", "--max-redirs",
		"--request", "--continue-at", "--time-cond", "--speed-limit",
		"--speed-time", "--quote", "--ftp-port", "--use-ascii",
		"--config", "--help", "--manual":
		return true
	}
	return false
}
