package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kairo/internal/logquery"
	"kairo/internal/sshclient"
)

type logsSearchReq struct {
	System      string `json:"system"`
	Server      string `json:"server"`
	Dir         string `json:"dir"`
	Files       int    `json:"files"` // 选最近几个文件
	Query       string `json:"query"` // 搜索表达式
	Username    string `json:"username"`
	Password    string `json:"password"`
	IgnoreCase  bool   `json:"ignore_case"`  // v0.13：忽略大小写搜索（透传 grep -i）
	MatchWindow int    `json:"match_window"` // v0.15：>0 时 && 走多行窗口匹配（awk），0 保持同行
}

func (s *Server) handleLogsSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	// BE-020：入口取一次配置快照，后续整个 handler 复用同一份，避免 TOCTOU。
	cur := s.cur()
	var req logsSearchReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	_, srv, ok := cur.FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
	ld, ok := findLogDir(srv, req.Dir)
	if !ok {
		writeErr(w, 400, errors.New("目录不在白名单中"))
		return
	}
	creds, err := s.resolveCreds(req.Username, req.Password, req.System, req.Server, srv.Username, srv.Password)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if creds.Password == "" {
		writeErr(w, 400, errors.New("缺少密码（输入或勾选「记住密码」）"))
		return
	}
	username := creds.Username
	kw, err := logquery.ParseQuery(req.Query)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	// B1：解析时间窗口过滤参数（query string 里 ?since=...&until=...）。
	// 两个都可单独省略；都省略 = 全量。
	tw, err := parseTimeWindow(r.URL.Query().Get("since"), r.URL.Query().Get("until"))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	filesN := req.Files
	if filesN <= 0 {
		filesN = cur.Search.DefaultLatestFiles
	}
	if filesN > 10 {
		filesN = 10
	}
	// v0.15：窗口匹配行数钳制（0 = 同行匹配；上限 50）
	matchWindow := req.MatchWindow
	if matchWindow < 0 {
		matchWindow = 0
	}
	if matchWindow > 50 {
		matchWindow = 50
	}

	// SSH Dial 独立 ctx + 统一超时（不受 SearchTimeout 太小影响）
	dialCtx, cancelDial := context.WithTimeout(r.Context(), sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: cur.App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
	cancelDial()
	if err != nil {
		auditErr(w, s.audit, "logs.search", "system", req.System, "server", req.Server, "dir", ld.Path, "query", req.Query, "result", "fail", err)
		writeErrSanitized(w, 502, err)
		return
	}
	defer cli.Close()

	// 列 N 个最新文件，再过滤 pattern 匹配的
	cmd, err := logquery.ListCommand(ld.Path, ld.Patterns, filesN, ld.ListModeFor())
	if err != nil {
		writeErrSanitized(w, 500, err)
		return
	}
	searchTimeout := cur.SearchTimeout()
	listCtx, cancelList := context.WithTimeout(r.Context(), searchTimeout+5*time.Second)
	stdout, stderr, code, err := cli.Run(listCtx, cmd, searchTimeout, ld.Encoding)
	cancelList()
	if err != nil || code != 0 {
		writeErrSanitized(w, 502, fmt.Errorf("列文件失败: %v", err))
		return
	}
	files, err := logquery.ParseListOutput(stdout)
	if err != nil || len(files) == 0 {
		writeJSON(w, 200, map[string]any{"hits": []any{}})
		return
	}
	fileNames := make([]string, 0, len(files))
	for _, f := range files {
		fileNames = append(fileNames, f.Name)
	}

	// v0.15：matchWindow>0 走多行窗口匹配，否则走原同行 grep 管道
	var searchCmd string
	if matchWindow > 0 {
		searchCmd, err = logquery.WindowSearchCommand(ld.Path, fileNames, kw, cur.Search.MaxMatches, cur.Search.TimeoutSeconds, ld.Encoding, req.IgnoreCase, matchWindow)
	} else {
		searchCmd, err = logquery.SearchCommand(ld.Path, fileNames, kw, cur.Search.MaxMatches, cur.Search.TimeoutSeconds, ld.Encoding, req.IgnoreCase)
	}
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	searchCtx, cancelSearch := context.WithTimeout(r.Context(), searchTimeout+15*time.Second)
	stdout, stderr, code, err = cli.Run(searchCtx, searchCmd, searchTimeout+5*time.Second, ld.Encoding)
	cancelSearch()
	if err != nil {
		s.audit.Write("logs.search", "system", req.System, "server", req.Server, "dir", ld.Path, "query", req.Query, "result", "fail", "err", err.Error())
		writeErrSanitized(w, 502, err)
		return
	}
	if code != 0 {
		// grep 没匹配到返回 1，是正常的
		if code == 1 && strings.TrimSpace(stdout) == "" {
			writeJSON(w, 200, map[string]any{"hits": []any{}})
			return
		}
		s.audit.Write("logs.search", "system", req.System, "server", req.Server, "dir", ld.Path, "query", req.Query, "result", "fail", "err", "exit="+strconv.Itoa(code), "stderr", trim(stderr, 200))
		writeErrSanitized(w, 502, fmt.Errorf("搜索失败: exit=%d %s", code, trim(stderr, 200)))
		return
	}

	hits := parseSearchOutput(stdout, srv.Name, ld.Path, files)
	// B1：按"文件 mtime"过滤命中（前后端都返回过滤后的 hits）
	hits = logquery.FilterHitsByTimeWindow(hits, files, tw)
	// v0.15.1：搜索结果只返回命中行，不再内嵌上下文——
	// 「上下文行数」输入框只作用于命中行右侧「上下文」按钮（走 /api/logs/context
	// 在新窗口展示前后 N 行），跟搜索接口解耦，避免单关键词搜索（如 "Kairo"）
	// 被强制补上 N 条不含关键词的日志。
	s.audit.Write("logs.search", "system", req.System, "server", req.Server, "dir", ld.Path, "query", req.Query, "result", "ok", "hits", len(hits), "window", matchWindow, "since", tw.Start.Format(time.RFC3339), "until", tw.End.Format(time.RFC3339))
	writeJSON(w, 200, map[string]any{
		"hits":  hits,
		"files": fileNames,
		"window": map[string]any{
			"since": timeOrEmpty(tw.Start),
			"until": timeOrEmpty(tw.End),
		},
	})
}

// parseTimeWindow 解析 since/until 字符串到 SearchTimeWindow。
//
// 规则：
//   - 空字符串 = 该端点不限；
//   - 非空 = RFC3339（如 "2026-06-23T00:00:00Z" 或带时区偏移）；
//   - 解析失败 = 400；
//   - since > until = 400（业务上不合理）。
//
// 注：原样不强制 UTC，由前端传什么时区就是什么时区；落库比较时 Go 会做
// 隐式时区换算（time.Time 是绝对时刻）。
func parseTimeWindow(sinceStr, untilStr string) (logquery.SearchTimeWindow, error) {
	var tw logquery.SearchTimeWindow
	sinceStr = strings.TrimSpace(sinceStr)
	untilStr = strings.TrimSpace(untilStr)
	if sinceStr != "" {
		t, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			return tw, fmt.Errorf("since 解析失败（需要 RFC3339，如 2026-06-23T00:00:00Z）: %w", err)
		}
		tw.Start = t
	}
	if untilStr != "" {
		t, err := time.Parse(time.RFC3339, untilStr)
		if err != nil {
			return tw, fmt.Errorf("until 解析失败（需要 RFC3339，如 2026-06-23T23:59:59Z）: %w", err)
		}
		tw.End = t
	}
	if !tw.Start.IsZero() && !tw.End.IsZero() && tw.Start.After(tw.End) {
		return tw, errors.New("since 必须早于 until")
	}
	return tw, nil
}

// timeOrEmpty 把零值 time 转空字符串，避免前端看到 "0001-01-01T00:00:00Z"。
func timeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func parseSearchOutput(out string, server, dir string, files []logquery.FileEntry) []logquery.SearchHit {
	// 建立 name -> file 映射（白名单），用来校验远端 grep 输出里的 filename
	// 必须是本次搜索范围内的文件。
	// 远端 grep 是按 fileList 跑的，正常不会出现白名单外的 file；
	// 但如果有人改 logquery.go 的 fileList 注入，或者 grep 本身拼错，
	// 这层校验可以兜底，避免前端解析出"看似合法但实际不在白名单"的命中。
	//
	// files 为 nil/空时跳过白名单校验（向后兼容旧测试 / 其他调用方）。
	byName := make(map[string]logquery.FileEntry, len(files))
	for _, f := range files {
		byName[f.Name] = f
	}
	useWhitelist := len(byName) > 0
	var hits []logquery.SearchHit
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// REVIEW-rc5 #1：filename 含 `:` 时的解析错位（与 logquery.parseSearchLine 同算法）。
		// 单一 search 接口只解析 hit 行（不含上下文 `-` 分隔符），sep 应当是 `:`；
		// 遇到 `-` 分隔的上下文行时按未知格式丢弃（这个 handler 本就不返回上下文行）。
		file, sep, ln, content, ok := logquery.ParseSearchHitLine(line)
		if !ok || sep != ':' {
			continue
		}
		// 防御性校验：file 必须来自 ListCommand 返回的 files 列表
		// （仅在 handler 提供 files 时启用）
		if useWhitelist {
			if _, ok := byName[file]; !ok {
				continue
			}
		}
		hits = append(hits, logquery.SearchHit{
			Server:   server,
			Dir:      dir,
			File:     file,
			FullPath: filepath.ToSlash(filepath.Join(dir, file)),
			LineNo:   ln,
			Content:  content,
		})
	}
	return hits
}

// ---------- /api/logs/context ----------

type logsContextReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Dir      string `json:"dir"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Before   *int   `json:"before"`
	After    *int   `json:"after"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogsContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	// BE-020：入口取一次配置快照，后续整个 handler 复用同一份，避免 TOCTOU。
	cur := s.cur()
	var req logsContextReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	_, srv, ok := cur.FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
	ld, ok := findLogDir(srv, req.Dir)
	if !ok {
		writeErr(w, 400, errors.New("目录不在白名单中"))
		return
	}
	creds, err := s.resolveCreds(req.Username, req.Password, req.System, req.Server, srv.Username, srv.Password)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if creds.Password == "" {
		writeErr(w, 400, errors.New("缺少密码（输入或勾选「记住密码」）"))
		return
	}
	username := creds.Username
	before := 0
	beforeSet := req.Before != nil
	if beforeSet {
		before = *req.Before
	}
	if !beforeSet || before < 0 {
		before = cur.Search.DefaultContextLines
	}
	after := 0
	afterSet := req.After != nil
	if afterSet {
		after = *req.After
	}
	if !afterSet || after < 0 {
		after = cur.Search.DefaultContextLines
	}
	if before > 5000 {
		before = 5000
	}
	if after > 5000 {
		after = 5000
	}
	cmd, err := logquery.ContextCommand(ld.Path, req.File, req.Line, before, after, cur.Search.TimeoutSeconds)
	if err != nil {
		writeErr(w, 400, err)
		return
	}

	// SSH Dial 独立 ctx + 统一超时
	dialCtx, cancelDial := context.WithTimeout(r.Context(), sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: cur.App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
	cancelDial()
	if err != nil {
		auditErr(w, s.audit, "logs.context", "system", req.System, "server", req.Server, "dir", ld.Path, "file", req.File, "line", req.Line, "result", "fail", err)
		writeErrSanitized(w, 502, err)
		return
	}
	defer cli.Close()

	searchTimeout := cur.SearchTimeout()
	runCtx, cancelRun := context.WithTimeout(r.Context(), searchTimeout+10*time.Second)
	defer cancelRun()
	stdout, stderr, code, err := cli.Run(runCtx, cmd, searchTimeout, ld.Encoding)
	if err != nil {
		s.audit.Write("logs.context", "system", req.System, "server", req.Server, "dir", ld.Path, "file", req.File, "line", req.Line, "result", "fail", "err", err.Error())
		writeErrSanitized(w, 502, err)
		return
	}
	if code != 0 {
		s.audit.Write("logs.context", "system", req.System, "server", req.Server, "dir", ld.Path, "file", req.File, "line", req.Line, "result", "fail", "err", "exit="+strconv.Itoa(code), "stderr", trim(stderr, 200))
		writeErrSanitized(w, 502, fmt.Errorf("sed 退出码 %d: %s", code, trim(stderr, 200)))
		return
	}
	lines := parseContextOutput(stdout, req.Line, before)
	s.audit.Write("logs.context", "system", req.System, "server", req.Server, "dir", ld.Path, "file", req.File, "line", req.Line, "result", "ok", "lines", len(lines))
	writeJSON(w, 200, map[string]any{
		"lines": lines,
		"file":  req.File,
		"line":  req.Line,
	})
}

func parseContextOutput(out string, hitLine, before int) []logquery.ContextLine {
	// sed -n 'a,bp' 的输出不带行号 —— 我们按输出行号重算
	var result []logquery.ContextLine
	arr := strings.Split(out, "\n")
	if len(arr) > 0 && strings.TrimRight(arr[len(arr)-1], "\r") == "" {
		arr = arr[:len(arr)-1]
	}
	startLine := hitLine - before
	if startLine < 1 {
		startLine = 1
	}
	for i, raw := range arr {
		raw = strings.TrimRight(raw, "\r")
		ln := startLine + i
		result = append(result, logquery.ContextLine{
			LineNo:  ln,
			Content: raw,
			Hit:     ln == hitLine,
		})
	}
	return result
}
