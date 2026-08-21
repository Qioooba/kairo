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
	"sync"
	"time"

	"kairo/internal/config"
	"kairo/internal/logquery"
	"kairo/internal/sshclient"
)

// ---------- /api/logs/list ----------

type logsListReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Dir      string `json:"dir"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	// BE-020：入口取一次配置快照，后续整个 handler 复用同一份，避免 TOCTOU。
	cur := s.cur()
	var req logsListReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	_, srv, ok := cur.FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
	// 找匹配的 log_dir（必须来自配置白名单）
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

	// SSH Dial 用独立 ctx + 统一超时，避免被 SearchTimeout 太小影响 3 套 profile 跑完。
	dialCtx, cancelDial := context.WithTimeout(r.Context(), sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: cur.App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
	cancelDial()
	if err != nil {
		auditErr(w, s.audit, "logs.list", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", err)
		writeErrSanitized(w, 502, err)
		return
	}
	defer cli.Close()

	cmd, err := logquery.ListCommand(ld.Path, ld.Patterns, 100, ld.ListModeFor())
	if err != nil {
		writeErrSanitized(w, 500, err)
		return
	}
	// Run 用 SearchTimeout 控制（不受 Dial ctx 影响）
	searchTimeout := cur.SearchTimeout()
	runCtx, cancelRun := context.WithTimeout(r.Context(), searchTimeout+10*time.Second)
	defer cancelRun()
	stdout, stderr, code, err := cli.Run(runCtx, cmd, searchTimeout, ld.Encoding)

	// 项 6：list_mode=auto 时，先试 gnu_find；远端不是 Linux/macOS / 没装 find，
	// 退出码非 0 或输出 "找不到命令" 时，降级到 posix_ls 再试一次。
	//
	// 注意：不能直接用 ListModeFor() 的归一化值判断（auto → gnu_find），
	// 必须用 ListModeIsAuto() 看用户是否显式选了具体模式。
	if ld.ListModeIsAuto() && shouldFallbackToPOSIX(stdout, stderr, code, err) {
		s.audit.Write("logs.list", "system", req.System, "server", req.Server, "dir", ld.Path,
			"stage", "fallback", "from", "gnu_find", "to", "posix_ls",
			"reason", trim(stderr, 100))
		fallbackCmd, ferr := logquery.ListCommand(ld.Path, ld.Patterns, 100, "posix_ls")
		if ferr == nil {
			stdout, stderr, code, err = cli.Run(runCtx, fallbackCmd, searchTimeout, ld.Encoding)
		}
	}

	if err != nil {
		auditErr(w, s.audit, "logs.list", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", err)
		writeErrSanitized(w, 502, err)
		return
	}
	if code != 0 {
		s.audit.Write("logs.list", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", "err", "remote exit="+strconv.Itoa(code), "stderr", trim(stderr, 200))
		writeErrSanitized(w, 502, fmt.Errorf("远程命令退出码 %d: %s", code, trim(stderr, 200)))
		return
	}
	files, err := logquery.ParseListOutput(stdout)
	if err != nil {
		writeErrSanitized(w, 500, err)
		return
	}
	// 补全 FullPath
	for i := range files {
		files[i].FullPath = filepath.ToSlash(filepath.Join(ld.Path, files[i].Name))
	}
	s.audit.Write("logs.list", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "ok", "count", strconv.Itoa(len(files)))
	writeJSON(w, 200, map[string]any{
		"files": files,
		"dir":   ld.Path,
		"name":  ld.Name,
	})
}

// findLogDir 在 server 的 log_dirs 中按 name 或 path 找
func findLogDir(srv *config.ServerConfig, key string) (*config.LogDirEntry, bool) {
	k := strings.TrimSpace(key)
	for i := range srv.LogDirs {
		ld := &srv.LogDirs[i]
		if ld.Name == k || ld.Path == k {
			return ld, true
		}
	}
	// 默认第一个
	if k == "" && len(srv.LogDirs) > 0 {
		return &srv.LogDirs[0], true
	}
	return nil, false
}

// shouldFallbackToPOSIX 判断 gnu_find 失败是否值得降级到 posix_ls。
//
// 触发条件（项 6）：
//   - 远端退出码非 0 且 stderr 提到典型的"命令不存在 / 选项不识别"信号；
//   - 比如 AIX 报 "0652-018" / "find: not found" / "command not found" /
//     BSD "unknown primary or operator" / "unknown option" —— 这些都说明远端
//     find 不可用或和我们假设的 -printf 用法不兼容，posix_ls（ls -lt）还有救。
//
// 不能只看 exit != 0 —— 文件确实不存在 / 没权限时 exit 也是非 0，但那种
// 情况下 posix_ls 也会失败，反复重试只会浪费 SSH 调用。
// 所以"find: .: Permission denied" 这种"文件级"错误不触发 fallback。
//
// 也不能简单"err != nil 就放弃 fallback" —— sshclient.Run 把所有非 0 退出
// 都包成 `ssh: command sh -c '...' failed` 错误，category=Command/Unknown，
// 这种"命令级"err 应该跟 nil 一样走 marker 判断。只有网络/超时/DNS/认证/
// host key/handshake/kbd-int 这类"重试也救不回来"的 err 才直接放弃。
func shouldFallbackToPOSIX(stdout, stderr string, code int, err error) bool {
	if code == 0 {
		return false
	}
	if err != nil {
		cat := sshclient.Categorize(err)
		switch cat {
		case sshclient.CatTimeout, sshclient.CatNetwork, sshclient.CatDNS, sshclient.CatPort,
			sshclient.CatAuth, sshclient.CatHostKey, sshclient.CatHandshake, sshclient.CatKbdInt:
			// 重试也救不回来，不该 fallback
			return false
		}
		// CatCommand / CatSFTP / CatUnknown：走 marker 判断
	}
	low := strings.ToLower(stderr)
	for _, marker := range []string{
		"find: not found",      // AIX / sh: find: not found
		"0652-",                // AIX find / getopt 错误码前缀
		"command not found",    // bash / sh 通用
		"unknown primary",      // macOS BSD find 不支持 -printf（"unknown primary or operator"）
		"unknown option",       // 兼容 AIX / 老 BSD 变体
		"paths must precede",   // GNU find 语法错（参数顺序）
		"invalid option",
		"unrecognized option",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

// ---------- /api/logs/list/targets ----------
//
// v0.5-G 用户原话 #10："日志助手这个页面，应该是可以勾选服务器，并且多选日志路径"，
// 多 targets 接口一次请求后端并发，避免前端 N+1 调 /api/logs/list（10+ 台时延迟明显）。
//
// 复用 /api/files/list 多 server 模式的 worker pool + per-server error 模式。

type logsListTargetsReq struct {
	System   string            `json:"system"`
	Targets  []logsListListTgt `json:"targets"` // 每项 {server, dir}
	Username string            `json:"username"`
	Password string            `json:"password"`
}

type logsListListTgt struct {
	Server string `json:"server"`
	Dir    string `json:"dir"`
}

type logsListTargetResult struct {
	Server string               `json:"server"`
	Host   string               `json:"host,omitempty"`
	Dir    string               `json:"dir"`
	OK     bool                 `json:"ok"`
	Error  string               `json:"error,omitempty"`
	Files  []logquery.FileEntry `json:"files,omitempty"`
	Count  int                  `json:"count"`
	Ms     int64                `json:"elapsed_ms"`
}

func (s *Server) handleLogsListTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req logsListTargetsReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	if strings.TrimSpace(req.System) == "" {
		writeErr(w, 400, errors.New("system 不能为空"))
		return
	}
	if len(req.Targets) == 0 {
		writeErr(w, 400, errors.New("targets 至少 1 个"))
		return
	}

	cur := s.cur()
	// 解析每个 target（找不到 server/dir → 错误结果）
	type job struct {
		idx    int
		server string
		dir    string
		srv    *config.ServerConfig
		ld     *config.LogDirEntry
	}
	results := make([]logsListTargetResult, len(req.Targets))
	jobs := make([]job, 0, len(req.Targets))
	for i, t := range req.Targets {
		sn := strings.TrimSpace(t.Server)
		dn := strings.TrimSpace(t.Dir)
		if sn == "" || dn == "" {
			results[i] = logsListTargetResult{
				Server: t.Server, Dir: t.Dir, OK: false, Error: "server/dir 不能为空", Count: 0,
			}
			continue
		}
		_, sc, ok := cur.FindServer(req.System, sn)
		if !ok {
			results[i] = logsListTargetResult{
				Server: sn, Dir: dn, OK: false, Error: "系统或服务器不存在", Count: 0,
			}
			continue
		}
		ld, ok := findLogDir(sc, dn)
		if !ok {
			results[i] = logsListTargetResult{
				Server: sn, Dir: dn, Host: sc.Host, OK: false, Error: "目录不在白名单中", Count: 0,
			}
			continue
		}
		results[i] = logsListTargetResult{Server: sn, Dir: ld.Path, Host: sc.Host, OK: true}
		jobs = append(jobs, job{idx: i, server: sn, dir: ld.Path, srv: sc, ld: ld})
	}

	if len(jobs) == 0 {
		writeJSON(w, 200, map[string]any{
			"servers":  results,
			"ok_count": 0, "fail_count": len(results), "total_count": 0,
		})
		return
	}

	// Worker pool（最多 4 路并发）
	jobCh := make(chan job, len(jobs))
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)

	concurrency := 4
	if concurrency > len(jobs) {
		concurrency = len(jobs)
	}
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				results[j.idx] = s.runOneLogsList(r.Context(), cur, req.System, j.srv, j.ld, req.Username, req.Password)
			}
		}()
	}
	wg.Wait()

	// 汇总
	okCount, failCount, totalCount := 0, 0, 0
	for _, res := range results {
		if res.OK {
			okCount++
			totalCount += res.Count
		} else {
			failCount++
		}
	}
	writeJSON(w, 200, map[string]any{
		"servers":     results,
		"ok_count":    okCount,
		"fail_count":  failCount,
		"total_count": totalCount,
	})
}

// runOneLogsList 单 target 列文件（抽出共享函数）
// cur 由调用方传入（BE-020：入口取一次配置快照，后续整个函数复用同一份，避免 TOCTOU）。
func (s *Server) runOneLogsList(parentCtx context.Context, cur *config.Config, system string, srv *config.ServerConfig, ld *config.LogDirEntry, username, password string) logsListTargetResult {
	start := time.Now()
	res := logsListTargetResult{Server: srv.Name, Host: srv.Host, Dir: ld.Path, OK: true}

	// 凭据自动回退（批量列表兼容修复）：共用密码框的手输密码被拒时，
	// 自动改用该服务器已保存/配置里的密码重试。
	dialCtx, cancelDial := context.WithTimeout(parentCtx, sshDialOuterTimeout)
	cli, _, err := s.dialSSHWithFallback(dialCtx, username, password, system, srv.Name, srv,
		cur.App.AllowInsecureHostKeyEnabled(), sshAttemptTimeout)
	cancelDial()
	if err != nil {
		res.OK = false
		res.Error = sshclient.SanitizeError(err.Error())
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	defer cli.Close()

	cmd, err := logquery.ListCommand(ld.Path, ld.Patterns, 100, ld.ListModeFor())
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	searchTimeout := cur.SearchTimeout()
	runCtx, cancelRun := context.WithTimeout(parentCtx, searchTimeout+10*time.Second)
	defer cancelRun()
	stdout, stderr, code, err := cli.Run(runCtx, cmd, searchTimeout, ld.Encoding)
	if ld.ListModeIsAuto() && shouldFallbackToPOSIX(stdout, stderr, code, err) {
		fallbackCmd, ferr := logquery.ListCommand(ld.Path, ld.Patterns, 100, "posix_ls")
		if ferr == nil {
			stdout, stderr, code, err = cli.Run(runCtx, fallbackCmd, searchTimeout, ld.Encoding)
		}
	}
	if err != nil {
		res.OK = false
		res.Error = sshclient.SanitizeError(err.Error())
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	if code != 0 {
		res.OK = false
		res.Error = "远程退出码 " + strconv.Itoa(code) + ": " + trim(stderr, 200)
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	files, err := logquery.ParseListOutput(stdout)
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	for i := range files {
		files[i].FullPath = filepath.ToSlash(filepath.Join(ld.Path, files[i].Name))
	}
	res.Files = files
	res.Count = len(files)
	res.Ms = time.Since(start).Milliseconds()
	return res
}
