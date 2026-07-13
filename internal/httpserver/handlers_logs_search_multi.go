package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"kairo/internal/config"
	"kairo/internal/logquery"
	"kairo/internal/sshclient"
)

// ---------- /api/logs/search/multi ----------
//
// 多服务器并行搜索，每台独立 SSH / 列文件 / 搜索 / 解析，
// 每台独立审计，超时独立计时。
//
// 并发控制：用有缓冲的 channel 当信号量，max 默认从 Search.MaxConcurrency 取，
// 若请求体里显式带 max_concurrency 字段则按它。
//
// 入参（v0.5 起支持多对多勾选，targets 字段）：
//
//	system           — 必填，业务系统名
//	servers          — [legacy] 多个 server name（必须配合 dir 字段，所有服务器共用同一目录）
//	dir              — [legacy] 日志目录的 name 或 path
//	targets          — [v0.5 新增] 多对多目标列表，元素 {server, dir}；与 servers/dir 互斥
//	files            — 每台搜索最近 N 个文件
//	query            — 搜索表达式
//	username/password — SSH 凭据（必须）
//	max_concurrency  — 可选，覆盖 Search.MaxConcurrency
//	file_patterns    — [v0.5 新增] 文件名 glob 列表（例：["*.log","SystemOut*.log"]），
//	                   为空时退回到按 patterns 过滤最近 N 个文件。
type logsSearchMultiReq struct {
	System         string             `json:"system"`
	Servers        []string           `json:"servers"`
	Dir            string             `json:"dir"`
	Targets        []logsSearchTarget `json:"targets"`
	Files          int                `json:"files"` // scope_mode=latest 时：每台搜索最近 N 个文件
	Query          string             `json:"query"`
	Context        int                `json:"context"`
	Username       string             `json:"username"`
	Password       string             `json:"password"`
	MaxConcurrency int                `json:"max_concurrency"`
	// v0.13：忽略大小写搜索。所有 grep 命令统一加 -i。
	//   - true  → 大小写不敏感（适合大小写不固定的英文关键词、混合日志）
	//   - false（默认）→ 大小写敏感，保持原行为
	IgnoreCase bool `json:"ignore_case"`
	// v0.5-G #8：搜索范围三种模式（互斥，优先级 selected > glob > latest）
	//   - "latest"（默认）：先 ListCommand 取最近 N 个文件，再搜索
	//   - "selected"：直接用 SelectedFiles 作为文件名列表，跳过 ListCommand
	//   - "glob"：用 FilePatterns 作为文件名 glob（跟 latest 一样列文件，但 patterns 覆盖 ld.Patterns）
	ScopeMode       string             `json:"scope_mode,omitempty"`  // "" = latest（向后兼容）
	SelectedFiles   []string           `json:"selected_files"`        // [legacy] 通用文件名列表，所有 target 共用（向后兼容）
	SelectedTargets []logsSelectedFile `json:"selected_file_targets"` // [v0.6 P1-9] 每条带 (server, dir, file)，按 target 精确指定；优先级高于 SelectedFiles
	FilePatterns    []string           `json:"file_patterns"`         // scope_mode=glob 时：文件名 glob 列表
}

// logsSelectedFile v0.6 P1-9：selected_files 的 per-target 形态
type logsSelectedFile struct {
	Server string `json:"server"`
	Dir    string `json:"dir"`
	File   string `json:"file"`
}

// logsSearchTarget 一个 (server, dir) 搜索目标
type logsSearchTarget struct {
	Server string `json:"server"`
	Dir    string `json:"dir"`
}

type logsSearchMultiServerResult struct {
	Server   string               `json:"server"`
	Host     string               `json:"host"`
	Dir      string               `json:"dir,omitempty"` // v0.5：方便前端区分多目录场景
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

// expandedTargets 把请求体展开成 [(server, dir), ...] 列表。
//   - 优先用 targets[] （v0.5 多对多）
//   - 兼容旧版 servers[] + dir
func (r *logsSearchMultiReq) expandedTargets() []logsSearchTarget {
	if len(r.Targets) > 0 {
		out := make([]logsSearchTarget, 0, len(r.Targets))
		for _, t := range r.Targets {
			if strings.TrimSpace(t.Server) != "" && strings.TrimSpace(t.Dir) != "" {
				out = append(out, t)
			}
		}
		return out
	}
	if len(r.Servers) > 0 && strings.TrimSpace(r.Dir) != "" {
		out := make([]logsSearchTarget, 0, len(r.Servers))
		for _, s := range r.Servers {
			out = append(out, logsSearchTarget{Server: s, Dir: r.Dir})
		}
		return out
	}
	return nil
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
	targets := req.expandedTargets()
	if len(targets) == 0 {
		writeErr(w, 400, errors.New("targets 至少一个（或兼容模式：servers + dir）"))
		return
	}
	// v0.5-G #8：scope_mode 请求级校验（不合法 → 400 整个请求）
	if rawScope := strings.TrimSpace(req.ScopeMode); rawScope != "" {
		switch strings.ToLower(rawScope) {
		case "latest", "selected", "glob":
			// ok
		default:
			writeErr(w, 400, errors.New("scope_mode 非法: "+rawScope+"（仅 latest / selected / glob）"))
			return
		}
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
	contextN := req.Context
	if contextN < 0 {
		contextN = 0
	}
	if contextN > 500 {
		contextN = 500
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
		// 优先用 targets 列表第一个 server 的用户名作为默认值
		if _, srv0, ok := cur.FindServer(req.System, targets[0].Server); ok && srv0.Username != "" {
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
	results := make([]logsSearchMultiServerResult, len(targets))
	var wg sync.WaitGroup

	for i, tgt := range targets {
		wg.Add(1)
		go func(idx int, target logsSearchTarget) {
			defer wg.Done()
			// 取信号
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-totalCtx.Done():
				results[idx] = logsSearchMultiServerResult{
					Server: target.Server, Dir: target.Dir, OK: false, Error: "总超时未开始",
				}
				return
			}

			_, srv, ok := cur.FindServer(req.System, target.Server)
			if !ok {
				results[idx] = logsSearchMultiServerResult{
					Server: target.Server, Dir: target.Dir, OK: false, Error: "系统或服务器不存在",
				}
				return
			}
			ld, ok := findLogDir(srv, target.Dir)
			if !ok {
				results[idx] = logsSearchMultiServerResult{
					Server: target.Server, Dir: target.Dir, Host: srv.Host, OK: false, Error: "目录不在白名单中",
				}
				return
			}
			// 每台服务器独立解析凭据（password 可来自请求或本机 keyring）
			c, cErr := s.resolveCreds(req.Username, req.Password, req.System, srv.Name, srv.Username, srv.Password)
			if cErr != nil {
				results[idx] = logsSearchMultiServerResult{Server: target.Server, Dir: target.Dir, Host: srv.Host, OK: false, Error: cErr.Error()}
				return
			}
			if c.Password == "" {
				results[idx] = logsSearchMultiServerResult{
					Server: target.Server, Dir: target.Dir, Host: srv.Host, OK: false,
					Error: "缺少密码（输入或勾选「记住密码」）",
				}
				return
			}
			// v0.5-G #8：scope_mode 三种模式（互斥，优先级 selected > glob > latest）
			scope := strings.ToLower(strings.TrimSpace(req.ScopeMode))
			if scope == "" {
				// 向后兼容：有 selected_files → selected；有 file_patterns → glob；否则 latest
				switch {
				case len(req.SelectedFiles) > 0:
					scope = "selected"
				case len(req.FilePatterns) > 0:
					scope = "glob"
				default:
					scope = "latest"
				}
			}
			// v0.6 P1-9：按 target (server, dir) 过滤 selected_file_targets；
			// 没匹配到再退到通用 SelectedFiles（向后兼容）。
			perTargetFiles := filterSelectedFilesForTarget(req.SelectedTargets, target)
			// 优先级：per-target 命中（即使空数组也算"显式空"）> 通用 SelectedFiles
			var useFiles []string
			hasPerTarget := false
			for _, t := range req.SelectedTargets {
				if t.Server == target.Server && t.Dir == target.Dir {
					hasPerTarget = true
					break
				}
			}
			if hasPerTarget {
				useFiles = perTargetFiles
			} else {
				useFiles = req.SelectedFiles
			}
			res := s.runOneServerSearchWithScope(totalCtx, srv, ld, scope, filesN, useFiles, req.FilePatterns, kw, c.Username, c.Password, tw, contextN, req.IgnoreCase)
			results[idx] = res
			// 审计
			if res.OK {
				// REVIEW-rc5 #6：检测 context_enrich_failed 标记（hit 主流程成功但上下文补全失败）
				if strings.HasPrefix(res.Error, "context_enrich_failed:") {
					s.audit.Write("logs.search.multi",
						"system", req.System, "server", target.Server, "dir", ld.Path,
						"query", req.Query, "result", "context_fail",
						"err", strings.TrimPrefix(res.Error, "context_enrich_failed: "),
						"hits", res.HitsN, "context", contextN, "ms", res.Ms)
				} else {
					s.audit.Write("logs.search.multi",
						"system", req.System, "server", target.Server, "dir", ld.Path,
						"query", req.Query, "result", "ok", "hits", res.HitsN, "ms", res.Ms)
				}
			} else {
				s.audit.Write("logs.search.multi",
					"system", req.System, "server", target.Server, "dir", ld.Path,
					"query", req.Query, "result", "fail", "err", res.Error, "ms", res.Ms)
			}
		}(i, tgt)
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

// runOneServerSearch 单台服务器的完整搜索流程（保留兼容旧逻辑）：
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
	return s.runOneServerSearchWithPatterns(ctx, srv, ld, ld.Patterns, filesN, kw, username, password)
}

// runOneServerSearchWithPatterns v0.5：支持覆盖 patterns（用于 file_patterns 字段）
func (s *Server) runOneServerSearchWithPatterns(
	ctx context.Context,
	srv *config.ServerConfig,
	ld *config.LogDirEntry,
	patterns []string,
	filesN int,
	kw []logquery.SearchKeyword,
	username, password string,
) logsSearchMultiServerResult {
	return s.runOneServerSearchWithScope(ctx, srv, ld, "latest", filesN, nil, patterns, kw, username, password, logquery.SearchTimeWindow{}, 0, false)
}

// runOneServerSearchWithScope v0.5-G #8：搜索范围三种模式（互斥）
//
//   - scope="latest"（默认）：先 ListCommand 取最近 N 个文件，再搜索
//   - scope="selected"：直接用 selectedFiles 作为文件名列表（精确指定），跳过 ListCommand
//   - scope="glob"：用 patterns 替换 ld.Patterns 列文件（行为跟 WithPatterns 一致）
//
// 安全：selected 模式下，文件名不能含路径分隔符（防止命令注入 / 路径穿越），
// 也不能是 . / ..，否则直接返回错误。
//
// tw 为时间窗口过滤（仅 latest/glob 模式有效）；contextN 为上下文行数（0 表示无上下文）。
// ignoreCase v0.13：忽略大小写搜索（透传到 SearchCommand，所有 grep 加 -i）。
func (s *Server) runOneServerSearchWithScope(
	ctx context.Context,
	srv *config.ServerConfig,
	ld *config.LogDirEntry,
	scope string,
	filesN int,
	selectedFiles []string,
	patterns []string,
	kw []logquery.SearchKeyword,
	username, password string,
	tw logquery.SearchTimeWindow,
	contextN int,
	ignoreCase bool,
) logsSearchMultiServerResult {
	start := time.Now()
	res := logsSearchMultiServerResult{
		Server: srv.Name, Host: srv.Host, Dir: ld.Path, Encoding: ld.Encoding,
	}

	// BE-020：入口取一次配置快照，后续整个函数复用同一份，避免 TOCTOU。
	cur := s.cur()
	// 单独给 Dial 一个短超时（10s），但仍受总 ctx 控制
	dialCtx, cancelDial := context.WithTimeout(ctx, sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: cur.App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: password}, sshAttemptTimeout)
	cancelDial()
	if err != nil {
		res.OK = false
		res.Error = sshclient.SanitizeError(err.Error())
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	defer cli.Close()

	// 决定 fileNames：
	//   - selected 模式：直接用 selectedFiles，跳过 ListCommand
	//   - glob 模式：跟 latest 一样列文件，但 patterns 覆盖
	//   - latest 模式：原行为
	var fileNames []string
	var files []logquery.FileEntry // for time-window filter (B1)

	switch scope {
	case "selected":
		// 安全：拒绝路径分隔符 / 绝对路径 / . / .. / 空 / 含 NUL
		safe := make([]string, 0, len(selectedFiles))
		for _, n := range selectedFiles {
			n = strings.TrimSpace(n)
			if n == "" || n == "." || n == ".." {
				continue
			}
			if strings.ContainsAny(n, "/\\\x00") || strings.HasPrefix(n, "-") {
				res.OK = false
				res.Error = "selected_files 含非法文件名: " + n
				res.Ms = time.Since(start).Milliseconds()
				return res
			}
			safe = append(safe, n)
		}
		if len(safe) == 0 {
			res.OK = true
			res.Hits = []logquery.SearchHit{}
			res.Files = []string{}
			res.Ms = time.Since(start).Milliseconds()
			return res
		}
		fileNames = safe
		// B1：selected 模式没有 mtime，跳过时间窗口过滤
	default:
		// latest / glob 模式：走 ListCommand
		listPatterns := ld.Patterns
		if scope == "glob" && len(patterns) > 0 {
			listPatterns = patterns
		}
		listCmd, err := logquery.ListCommand(ld.Path, listPatterns, filesN, ld.ListModeFor())
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
		files, err = logquery.ParseListOutput(stdout)
		if err != nil || len(files) == 0 {
			// 没文件不算错，只是没结果
			res.OK = true
			res.Hits = []logquery.SearchHit{}
			res.Files = []string{}
			res.Ms = time.Since(start).Milliseconds()
			return res
		}
		fileNames = make([]string, 0, len(files))
		for _, f := range files {
			fileNames = append(fileNames, f.Name)
		}
	}

	// 搜索
	searchCmd, err := logquery.SearchCommand(ld.Path, fileNames, kw, cur.Search.MaxMatches, cur.Search.TimeoutSeconds, ld.Encoding, ignoreCase)
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
	// B1：按文件 mtime 过滤命中（非 selected 模式，selected 模式无 mtime 信息）
	if scope != "selected" && len(hits) > 0 {
		hits = logquery.FilterHitsByTimeWindow(hits, files, tw)
	}
	// 排序：文件按 mtime 降序，同文件内按行号降序（与 ParseContextEnrichedOutput 一致）
	if len(hits) > 0 {
		byName := make(map[string]logquery.FileEntry, len(files))
		for _, f := range files {
			byName[f.Name] = f
		}
		sort.SliceStable(hits, func(i, j int) bool {
			fi, oki := byName[hits[i].File]
			fj, okj := byName[hits[j].File]
			if oki && okj {
				ti, tj := fi.ModTimeParsed(), fj.ModTimeParsed()
				if !ti.IsZero() && !tj.IsZero() {
					if !ti.Equal(tj) {
						return ti.After(tj)
					}
				}
			}
			return hits[i].LineNo > hits[j].LineNo
		})
	}
	// 上下文行：contextN > 0 时为每个匹配行获取前后 N 行
	// REVIEW-rc5 #6：失败不再静默回退，把 err 信息塞到 result.Error（前端可见），
	// audit 由 caller 负责（需要 req/target 闭包，这里拿不到）。
	if contextN > 0 && len(hits) > 0 {
		enriched, ctxErr := s.enrichHitsWithContext(ctx, cli, ld, srv.Name, files, hits, contextN)
		if ctxErr != nil {
			res.OK = true // 不阻断主流程，hits 仍返回
			res.Hits = hits
			res.Files = fileNames
			res.HitsN = len(hits)
			res.Ms = time.Since(start).Milliseconds()
			res.fileList = files
			// 在 err 字段里塞一个标记（前端可读：hits 没上下文，但 search 主流程成功）
			res.Error = "context_enrich_failed: " + ctxErr.Error()
			return res
		}
		hits = enriched
	}
	res.OK = true
	res.Hits = hits
	res.Files = fileNames
	res.HitsN = len(hits)
	res.Ms = time.Since(start).Milliseconds()
	res.fileList = files
	return res
}

// filterSelectedFilesForTarget v0.6 P1-9：从 per-target 列表里筛选出匹配
// (server, dir) 的 file 列表（v0.13：不区分大小写匹配 server/dir，跟 Windows
// 文件系统语义一致；file 名做 trim 兜底）。
// 调用方需先判断 hasPerTarget（命中）再决定是否使用。
func filterSelectedFilesForTarget(items []logsSelectedFile, target logsSearchTarget) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		// REVIEW-rc5 #2：原实现用 == 比较 server/dir，前端大小写不一致时会
		// 静默 fallback 到 latest 模式，用户看不到自己选的文件。改成 EqualFold
		// 对齐 Windows 文件系统语义（用户/前端不该被强制记配置的大小写）。
		if strings.EqualFold(it.Server, target.Server) && strings.EqualFold(it.Dir, target.Dir) {
			f := strings.TrimSpace(it.File)
			if f != "" {
				out = append(out, f)
			}
		}
	}
	return out
}
