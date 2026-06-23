package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"ops-toolbox/internal/config"
	"ops-toolbox/internal/logquery"
	"ops-toolbox/internal/sshclient"
)

// ---------- /api/logs/search/multi ----------
//
// 多服务器并行搜索，每台独立 SSH / 列文件 / 搜索 / 解析，
// 每台独立审计，超时独立计时。
//
// 并发控制：用有缓冲的 channel 当信号量，max 默认从 Search.MaxConcurrency 取，
// 若请求体里显式带 max_concurrency 字段则按它。
//
// 入参：
//
//	system           — 必填，业务系统名
//	servers          — 必填，多个 server name
//	dir              — 必填，日志目录的 name 或 path
//	files            — 每台搜索最近 N 个文件
//	query            — 搜索表达式
//	username/password — SSH 凭据（必须）
//	max_concurrency  — 可选，覆盖 Search.MaxConcurrency
type logsSearchMultiReq struct {
	System         string   `json:"system"`
	Servers        []string `json:"servers"`
	Dir            string   `json:"dir"`
	Files          int      `json:"files"`
	Query          string   `json:"query"`
	Username       string   `json:"username"`
	Password       string   `json:"password"`
	MaxConcurrency int      `json:"max_concurrency"`
}

type logsSearchMultiServerResult struct {
	Server   string               `json:"server"`
	Host     string               `json:"host"`
	Encoding string               `json:"encoding,omitempty"`
	OK       bool                 `json:"ok"`
	Error    string               `json:"error,omitempty"`
	Hits     []logquery.SearchHit `json:"hits,omitempty"`
	Files    []string             `json:"files,omitempty"`
	HitsN    int                  `json:"hits_count"`
	Ms       int64                `json:"elapsed_ms"`
	// fileList 用于 B1 时间窗口过滤：保留每台服务器 ListCommand 解析出的
	// 完整 FileEntry（带 ModTime），过滤完后再隐藏（不返给前端）。
	// 反序列化时为空数组不影响 JSON 输出（隐藏字段不输出）。
	fileList []logquery.FileEntry `json:"-"`
}

func (s *Server) handleLogsSearchMulti(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req logsSearchMultiReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	if strings.TrimSpace(req.System) == "" {
		writeErr(w, 400, errors.New("system 不能为空"))
		return
	}
	if len(req.Servers) == 0 {
		writeErr(w, 400, errors.New("servers 至少一个"))
		return
	}
	if strings.TrimSpace(req.Dir) == "" {
		writeErr(w, 400, errors.New("dir 不能为空"))
		return
	}
	kw, err := logquery.ParseQuery(req.Query)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	// B1：解析时间窗口过滤参数（query string 里 ?since=...&until=...）。
	tw, err := parseTimeWindow(r.URL.Query().Get("since"), r.URL.Query().Get("until"))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	cur := s.cur()
	filesN := req.Files
	if filesN <= 0 {
		filesN = cur.Search.DefaultLatestFiles
	}
	if filesN > 10 {
		filesN = 10
	}
	maxConc := req.MaxConcurrency
	if maxConc <= 0 {
		maxConc = cur.Search.MaxConcurrency
	}
	if maxConc <= 0 {
		maxConc = 8 // 全局兜底
	}
	if maxConc > 32 {
		maxConc = 32
	}
	// 解析凭据的用户名（页面可留空，使用配置默认）
	username := strings.TrimSpace(req.Username)
	if username == "" {
		// 优先用 servers 列表第一台的用户名作为默认值
		if _, srv0, ok := cur.FindServer(req.System, req.Servers[0]); ok && srv0.Username != "" {
			username = srv0.Username
		}
	}
	if username == "" {
		writeErr(w, 400, errors.New("缺少用户名"))
		return
	}

	// 整体超时：每台 search_timeout + 一点缓冲，再乘以 max(并发比, 1) 作为下限。
	// 实际由 ctx.WithTimeout + goroutine 内部的 Run 共同兜底。
	perServer := cur.SearchTimeout() + 15*time.Second
	totalCtx, cancel := context.WithTimeout(r.Context(), perServer+30*time.Second)
	defer cancel()

	// 信号量
	sem := make(chan struct{}, maxConc)
	results := make([]logsSearchMultiServerResult, len(req.Servers))
	var wg sync.WaitGroup

	for i, srvName := range req.Servers {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()
			// 取信号
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-totalCtx.Done():
				results[idx] = logsSearchMultiServerResult{
					Server: name, OK: false, Error: "总超时未开始",
				}
				return
			}

			_, srv, ok := cur.FindServer(req.System, name)
			if !ok {
				results[idx] = logsSearchMultiServerResult{
					Server: name, OK: false, Error: "系统或服务器不存在",
				}
				return
			}
			ld, ok := findLogDir(srv, req.Dir)
			if !ok {
				results[idx] = logsSearchMultiServerResult{
					Server: name, Host: srv.Host, OK: false, Error: "目录不在白名单中",
				}
				return
			}
			// 每台服务器独立解析凭据（password 可来自请求或本机 keyring）
			c, cErr := s.resolveCreds(req.Username, req.Password, req.System, srv.Name, srv.Username)
			if cErr != nil {
				results[idx] = logsSearchMultiServerResult{Server: name, Host: srv.Host, OK: false, Error: cErr.Error()}
				return
			}
			if c.Password == "" {
				results[idx] = logsSearchMultiServerResult{
					Server: name, Host: srv.Host, OK: false,
					Error: "缺少密码（输入或勾选「记住密码」）",
				}
				return
			}
			res := s.runOneServerSearch(totalCtx, srv, ld, filesN, kw, c.Username, c.Password)
			// B1：按文件 mtime 过滤命中
			if res.OK && len(res.Hits) > 0 {
				res.Hits = logquery.FilterHitsByTimeWindow(res.Hits, res.fileList, tw)
				res.HitsN = len(res.Hits)
			}
			results[idx] = res
			// 审计
			if res.OK {
				s.audit.Write("logs.search.multi",
					"system", req.System, "server", name, "dir", ld.Path,
					"query", req.Query, "result", "ok", "hits", res.HitsN, "ms", res.Ms)
			} else {
				s.audit.Write("logs.search.multi",
					"system", req.System, "server", name, "dir", ld.Path,
					"query", req.Query, "result", "fail", "err", res.Error, "ms", res.Ms)
			}
		}(i, srvName)
	}
	wg.Wait()

	// 汇总
	okN, totalHits := 0, 0
	for _, r := range results {
		if r.OK {
			okN++
			totalHits += r.HitsN
		}
	}
	writeJSON(w, 200, map[string]any{
		"servers":         results,
		"ok_count":        okN,
		"fail_count":      len(results) - okN,
		"total_hits":      totalHits,
		"max_concurrency": maxConc,
	})
}

// runOneServerSearch 单台服务器的完整搜索流程：
// Dial → 列文件 → 过滤 patterns → 构造 search 命令 → Run → parse。
// 出错时把 err 写到结果里，不 panic；ctx 取消时优雅退出。
func (s *Server) runOneServerSearch(
	ctx context.Context,
	srv *config.ServerConfig,
	ld *config.LogDirEntry,
	filesN int,
	kw []logquery.SearchKeyword,
	username, password string,
) logsSearchMultiServerResult {
	start := time.Now()
	res := logsSearchMultiServerResult{
		Server: srv.Name, Host: srv.Host, Encoding: ld.Encoding,
	}

	// 单独给 Dial 一个短超时（10s），但仍受总 ctx 控制
	dialCtx, cancelDial := context.WithTimeout(ctx, sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
	}, sshclient.Credentials{Password: password}, sshAttemptTimeout)
	cancelDial()
	if err != nil {
		res.OK = false
		res.Error = sshclient.SanitizeError(err.Error())
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	defer cli.Close()

	cur := s.cur()

	// 列 N 个最新文件
	listCmd, err := logquery.ListCommand(ld.Path, ld.Patterns, filesN, ld.ListModeFor())
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	listCtx, cancelList := context.WithTimeout(ctx, cur.SearchTimeout()+5*time.Second)
	stdout, _, code, err := cli.Run(listCtx, listCmd, cur.SearchTimeout(), ld.Encoding)
	cancelList()
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	if code != 0 {
		res.OK = false
		res.Error = "列文件失败 exit=" + strconv.Itoa(code)
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	files, err := logquery.ParseListOutput(stdout)
	if err != nil || len(files) == 0 {
		// 没文件不算错，只是没结果
		res.OK = true
		res.Hits = []logquery.SearchHit{}
		res.Files = []string{}
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	fileNames := make([]string, 0, len(files))
	for _, f := range files {
		fileNames = append(fileNames, f.Name)
	}

	// 搜索
	searchCmd, err := logquery.SearchCommand(ld.Path, fileNames, kw, cur.Search.MaxMatches, cur.Search.TimeoutSeconds, ld.Encoding)
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	searchCtx, cancelSearch := context.WithTimeout(ctx, cur.SearchTimeout()+10*time.Second)
	stdout, stderr, code, err := cli.Run(searchCtx, searchCmd, cur.SearchTimeout()+5*time.Second, ld.Encoding)
	cancelSearch()
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	if code != 0 {
		// grep exit=1 是"无匹配"，是正常的
		if code == 1 && strings.TrimSpace(stdout) == "" {
			res.OK = true
			res.Hits = []logquery.SearchHit{}
			res.Files = fileNames
			res.Ms = time.Since(start).Milliseconds()
			return res
		}
		res.OK = false
		res.Error = "搜索失败 exit=" + strconv.Itoa(code) + " " + trim(stderr, 200)
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	hits := parseSearchOutput(stdout, srv.Name, ld.Path, files)
	res.OK = true
	res.Hits = hits
	res.Files = fileNames
	res.HitsN = len(hits)
	res.Ms = time.Since(start).Milliseconds()
	res.fileList = files // B1：保留给时间窗口过滤
	return res
}
