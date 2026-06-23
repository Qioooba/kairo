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

	"ops-toolbox/internal/config"
	"ops-toolbox/internal/logquery"
	"ops-toolbox/internal/sshclient"
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
	var req logsListReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	_, srv, ok := s.cur().FindServer(req.System, req.Server)
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
	creds, err := s.resolveCreds(req.Username, req.Password, req.System, req.Server, srv.Username)
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
	runCtx, cancelRun := context.WithTimeout(r.Context(), s.cur().SearchTimeout()+10*time.Second)
	defer cancelRun()
	stdout, stderr, code, err := cli.Run(runCtx, cmd, s.cur().SearchTimeout(), ld.Encoding)

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
			stdout, stderr, code, err = cli.Run(runCtx, fallbackCmd, s.cur().SearchTimeout(), ld.Encoding)
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
//     BSD "unknown option" —— 这些都说明远端 find 不可用或和我们假设的
//     -printf 用法不兼容，posix_ls（ls -lt）还有救。
//
// 不能只看 exit != 0 —— 文件确实不存在 / 没权限时 exit 也是非 0，但那种
// 情况下 posix_ls 也会失败，反复重试只会浪费 SSH 调用。
// 所以"find: .: Permission denied" 这种"文件级"错误不触发 fallback。
func shouldFallbackToPOSIX(stdout, stderr string, code int, err error) bool {
	if code == 0 {
		return false
	}
	if err != nil {
		// 网络 / 超时类失败：不该 fallback，下一次也是同个错
		return false
	}
	low := strings.ToLower(stderr)
	for _, marker := range []string{
		"find: not found",    // AIX / sh: find: not found
		"0652-",              // AIX find / getopt 错误码前缀
		"command not found",  // bash / sh 通用
		"unknown option",     // BSD find 不支持 -printf
		"paths must precede", // GNU find 语法错（参数顺序）
		"invalid option",
		"unrecognized option",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}
