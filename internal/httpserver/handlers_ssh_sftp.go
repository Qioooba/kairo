package httpserver

// handlers_ssh_sftp.go — SSH 终端 tab 内嵌的 SFTP 文件浏览器后端。
//
// 与 handlers_files.go 的差别：
//   - 不走 app.free_file_roots 白名单（用户在 SSH 终端已能 cd 到任何路径，
//     限制 SFTP 路径没意义；访问控制由 SSH 账号权限承担）；
//   - 不复用 /api/files/* 路由，独立 /api/ssh/sftp/* 命名空间，便于将来
//     扩展「共享 SSH 连接」（Phase 2：在此路由加 session_id 优先级）；
//   - 凭据复用 resolveCreds helper（keyring / file / 配置默认值三级降级）；
//   - SFTP 客户端复用 sftpclient.NewAuto（SFTP + shell 兜底，兼容老 AIX）；
//   - 下载进度复用 dlmanager.Session + SSE（与 files 页一致）；
//   - 严格只读：list / preview / download / pwd，无写/删/改权限接口
//     （符合 sftpclient 的安全约束）。
//
// Phase 1（当前）：每次请求独立 Dial，不复用 ws session 的 SSH 连接。
// Phase 2（规划）：扩展 sshshell.Manager 持有 *sshclient.Client，
//                 本路由支持 session_id 优先于 (system, server)。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"

	"kairo/internal/config"
	"kairo/internal/dlmanager"
	"kairo/internal/downloads"
	"kairo/internal/sftpclient"
	"kairo/internal/sshclient"
)

// ============================================================================
// 请求/响应类型
// ============================================================================

// sshSftpListReq 列目录请求
//
// OTH-05：契约区分 display_path（人类可读，地址栏/面包屑/审计）与
// path_id（不透明身份，`kairo-raw:<hex>`）。旧 path 字段保留兼容。
type sshSftpListReq struct {
	System      string `json:"system"`
	Server      string `json:"server"`
	Username    string `json:"username"` // 可选；空则用 server 配置默认
	Password    string `json:"password"` // 可选；空则走 resolveCreds（keyring/file/配置）
	Path        string `json:"path"`     // 旧契约：展示绝对路径
	DisplayPath string `json:"display_path,omitempty"`
	PathID      string `json:"path_id,omitempty"` // 身份：优先于 path
}

// sshSftpEntry 目录条目（与 filesEntry 一致，便于前端复用渲染逻辑）
//
// path_id 对**所有**条目都下发（含 ASCII/UTF-8/GBK）：只给非 UTF-8 条目发身份，
// 会让同显示名的 UTF-8 兄弟条目没有可区别于展示路径的标识。
type sshSftpEntry struct {
	Name        string `json:"name"`
	DisplayPath string `json:"display_path,omitempty"`
	RawName     string `json:"raw_name,omitempty"`
	Encoding    string `json:"encoding,omitempty"`
	PathID      string `json:"path_id,omitempty"`
	Size        int64  `json:"size"`
	IsDir       bool   `json:"isDir"`
	Mode        string `json:"mode"`
	MTime       string `json:"mtime"`
}

// sshSftpListResp 列目录响应
type sshSftpListResp struct {
	Path         string         `json:"path"`
	DisplayPath  string         `json:"display_path,omitempty"`
	Parent       string         `json:"parent"`
	PathID       string         `json:"path_id,omitempty"`
	ParentPathID string         `json:"parent_path_id,omitempty"`
	Entries      []sshSftpEntry `json:"entries"`
	Backend      string         `json:"backend"` // "sftp" | "shell"（兜底）
	ElapsedMs    int64          `json:"elapsed_ms"`
}

// sshSftpPwdReq 获取当前目录请求
type sshSftpPwdReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// sshSftpPwdResp pwd 响应
type sshSftpPwdResp struct {
	Path string `json:"path"`
}

// sshSftpPreviewReq 预览请求
type sshSftpPreviewReq struct {
	System      string `json:"system"`
	Server      string `json:"server"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Path        string `json:"path"`
	DisplayPath string `json:"display_path,omitempty"`
	PathID      string `json:"path_id,omitempty"`
	Encoding    string `json:"encoding,omitempty"`
	MaxBytes    int64  `json:"max_bytes,omitempty"`
}

// sshSftpDownloadReq 下载请求
//
// PathIDs 与 Paths 平行（同长度同序）：Paths 用于本地命名/进度事件（人类可读），
// PathIDs 决定真正访问哪个物理条目。
type sshSftpDownloadReq struct {
	System    string   `json:"system"`
	Server    string   `json:"server"`
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	Paths     []string `json:"paths"`
	PathIDs   []string `json:"path_ids,omitempty"`
	Zip       bool     `json:"zip"`
	TargetDir string   `json:"target_dir,omitempty"`
}

// ----------------------------------------------------------------------------
// OTH-05：HTTP 身份契约的唯一入口
// ----------------------------------------------------------------------------
//
// 所有需要"远端路径"的 handler 都必须经由下面这组函数：
//   - 先解码身份（path_id / parent_path_id），得到**原始字节绝对路径**，
//     再按原始路径做合法性校验（绝对、无 NUL/CR/LF）——不放宽既有非法路径校验；
//   - 没有身份字段时回退旧契约（path/display_path 都是展示路径，必须是绝对路径）；
//   - 展示路径只用于 UI / 审计 / 本地命名，绝不参与物理定位。

// sftpValidateRawPath 校验身份解码出的原始绝对路径。
func sftpValidateRawPath(raw, field string) error {
	if raw == "" {
		return fmt.Errorf("%s 不能为空", field)
	}
	if !strings.HasPrefix(raw, "/") {
		return fmt.Errorf("%s 解码后必须是绝对路径", field)
	}
	if strings.ContainsAny(raw, "\x00\r\n") {
		return fmt.Errorf("%s 含非法字符", field)
	}
	return nil
}

// sftpRequestPath 解析"单个远端路径"的请求字段。
// 返回 (identity, display, err)：identity 交给 sftpclient，display 给人看。
//
// 关键安全点：有身份时，展示路径**由身份解码后的原始字节推导**，不采用请求里的
// display_path。否则客户端可以"白名单内的展示路径 + 越权身份"绕过 free_file_roots
// 之类的按展示路径做的校验。
func sftpRequestPath(pathID, displayPath, legacyPath string) (string, string, error) {
	pathID = strings.TrimSpace(pathID)
	displayPath = strings.TrimSpace(displayPath)
	legacyPath = strings.TrimSpace(legacyPath)

	if pathID != "" {
		if raw, ok := sftpclient.DecodePathIdentity(pathID); ok {
			if err := sftpValidateRawPath(raw, "path_id"); err != nil {
				return "", "", err
			}
			// P2（审核第 10 项）：这里必须把**身份 token** 原样交给 sftpclient，
			// 展示名单独算（原始字节 → 人类可读，地址栏不显示 token）。
			//
			// 旧实现返回解码后的原始路径，sftpclient 收到"看起来像展示路径"的原始字节后
			// 会重新展开 [UTF-8, GBK] 编码候选：同目录下存在同显示名、不同字节编码的两个
			// 条目时就是 ErrAmbiguousPath —— 用户明明已经在列表里选中了精确条目，
			// 预览/下载却报"路径歧义"；GBK 条目的原始字节还会被当成展示名带进本地命名链路。
			// token 会被客户端直接解码回同一份原始字节（零远端解析、无歧义）。
			return sftpclient.EncodePathIdentity(raw), sftpclient.DecodeServerName(raw), nil
		}
		// 不是合法身份 token：宽容地按"展示绝对路径"处理（与旧契约同一套校验）。
		// 这样老前端把展示路径填进 path_id 也能工作，且校验强度不变
		// （授权用的是由该值推导出的展示路径，客户端无法用 display_path 覆盖）。
		legacyPath = pathID
	}

	p := legacyPath
	if p == "" {
		p = displayPath
	}
	if p == "" {
		return "", "", errors.New("path 不能为空")
	}
	if !strings.HasPrefix(p, "/") {
		return "", "", fmt.Errorf("path 必须是绝对路径: %q", p)
	}
	if strings.ContainsAny(p, "\x00\n\r") {
		return "", "", fmt.Errorf("path 含非法字符: %q", p)
	}
	return p, p, nil
}

// sftpParentIdentity 解析"父目录身份"：优先 parent_path_id（身份 token），
// 回退到展示绝对路径（旧契约）。返回 (原始父路径, 展示父路径, err)。
func sftpParentIdentity(parentPathID, legacyParent string) (string, string, error) {
	parentPathID = strings.TrimSpace(parentPathID)
	legacyParent = strings.TrimSpace(legacyParent)

	if parentPathID != "" {
		if raw, ok := sftpclient.DecodePathIdentity(parentPathID); ok {
			if err := sftpValidateRawPath(raw, "parent_path_id"); err != nil {
				return "", "", err
			}
			return raw, sftpclient.DecodeServerName(raw), nil
		}
		// 前端在没有 path_id 的场景（例如自定义 backend）会退回展示路径，
		// 这里按旧契约校验；歧义仍会在 sftpclient 解析阶段被拒绝。
		legacyParent = parentPathID
	}
	if legacyParent == "" {
		return "", "", errors.New("parent_path_id / path 不能为空")
	}
	if !strings.HasPrefix(legacyParent, "/") {
		return "", "", fmt.Errorf("父目录必须是绝对路径: %q", legacyParent)
	}
	if strings.ContainsAny(legacyParent, "\x00\n\r") {
		return "", "", fmt.Errorf("父目录含非法字符: %q", legacyParent)
	}
	return legacyParent, legacyParent, nil
}

// sftfSafeChildName 校验新建/重命名用的"单个名字段"。
// 服务端组合 parent_path_id + name，绝不让调用方拼 "/name" 或 ".partial" 到身份 token 上。
func sftpSafeChildName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name 不能为空")
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("name 非法: %q", name)
	}
	if strings.ContainsAny(name, "/\\\x00\r\n") {
		return "", fmt.Errorf("name 含非法字符: %q", name)
	}
	return name, nil
}

// sftpCreateTarget 组合 (parent_path_id + name) 或回退旧 (path_id/path) 契约，
// 返回 (identity, display, err)。
func sftpCreateTarget(parentPathID, name, pathID, legacyPath string) (string, string, error) {
	hasParent := strings.TrimSpace(parentPathID) != ""
	hasName := strings.TrimSpace(name) != ""
	if hasParent || hasName {
		parentRaw, parentDisplay, err := sftpParentIdentity(parentPathID, "")
		if err != nil {
			return "", "", err
		}
		child, err := sftpSafeChildName(name)
		if err != nil {
			return "", "", err
		}
		// 组合结果同样以身份 token 下发：父目录是按原始字节解析出来的，
		// 若把原始字节路径交回客户端，写入前还会再解析一次父目录（可能歧义）。
		// token 会让客户端直接拿到这份精确的原始字节路径。
		return sftpclient.EncodePathIdentity(path.Join(parentRaw, child)), path.Join(parentDisplay, child), nil
	}
	return sftpRequestPath(pathID, "", legacyPath)
}

// sftpChildPath 把"父目录原始路径 + 新名字"组合成目标路径（服务端组合，不在前端拼串）。
func sftpChildPath(parentRaw, name string) (string, error) {
	child, err := sftpSafeChildName(name)
	if err != nil {
		return "", err
	}
	return path.Join(parentRaw, child), nil
}

// sftpDisplayNameOf 把"访问目标"（身份 token 或展示路径）转成人类可读路径。
// 用于本地命名 / 进度事件 / 审计：绝不让 token 出现在用户可见的地方。
func sftpDisplayNameOf(target string) string {
	if raw, ok := sftpclient.DecodePathIdentity(target); ok {
		return sftpclient.DecodeServerName(raw)
	}
	return target
}

// sftpDirIdentities 由 sftpclient 已解析的原始目录生成目录自身 / 父目录身份。
// 自定义 backend（测试替身）不支持时返回空串，前端退回展示路径。
func sftpDirIdentities(ctx context.Context, cli sftpClientLike, display string) (string, string) {
	resolver, ok := cli.(interface {
		ResolveDirIdentityCtx(context.Context, string) (string, error)
	})
	if !ok {
		return "", ""
	}
	raw, err := resolver.ResolveDirIdentityCtx(ctx, display)
	if err != nil || raw == "" {
		return "", ""
	}
	return sftpclient.EncodePathIdentity(raw), sftpclient.EncodePathIdentity(path.Dir(raw))
}

// sftpCtxStat / sftpCtxOpen / sftpCtxReadDir / sftpCtxListLimited：优先使用
// sftpclient 的 ctx 变体（昂贵解析阶段可取消），自定义 backend 退回无 ctx 版本。
func sftpCtxStat(ctx context.Context, cli sftpClientLike, p string) (os.FileInfo, error) {
	if c, ok := cli.(interface {
		StatCtx(context.Context, string) (os.FileInfo, error)
	}); ok {
		return c.StatCtx(ctx, p)
	}
	return cli.Stat(p)
}

func sftpCtxOpen(ctx context.Context, cli sftpClientLike, p string) (sftpclient.SftpFile, error) {
	if c, ok := cli.(interface {
		OpenCtx(context.Context, string) (sftpclient.SftpFile, error)
	}); ok {
		return c.OpenCtx(ctx, p)
	}
	return cli.Open(p)
}

func sftpCtxListLimited(ctx context.Context, cli sftpClientLike, p string, max int) ([]os.FileInfo, bool, error) {
	if c, ok := cli.(interface {
		ListLimitedCtx(context.Context, string, int) ([]os.FileInfo, bool, error)
	}); ok {
		return c.ListLimitedCtx(ctx, p, max)
	}
	return cli.ListLimited(p, max)
}

func sftpCtxReadDir(ctx context.Context, cli sftpClientLike, p string) ([]os.FileInfo, error) {
	if c, ok := cli.(interface {
		ReadDirCtx(context.Context, string) ([]os.FileInfo, error)
	}); ok {
		return c.ReadDirCtx(ctx, p)
	}
	return cli.ReadDir(p)
}

// ============================================================================
// 常量（与 handlers_files.go 对齐，便于一致行为）
// ============================================================================

const (
	sshSftpMaxFilesPerTask = 100
	sshSftpPwdTimeout      = 8 * time.Second // pwd 是轻量命令，给 8s 足够（含 dial）
)

// resolveSshSftpCreds 是 /api/ssh/sftp/* 系列 handler 共用的凭据解析 helper。
// 4 个 handler（list/pwd/preview/download）入参都是 (system, server, username, password)，
// 共享「FindServer → resolveCreds → 校验 password 非空」的三段式校验。
// 成功返回 (srv, creds, true)；失败时已 writeErr 并返回 false，调用方应直接 return。
func (s *Server) resolveSshSftpCreds(
	w http.ResponseWriter,
	system, server, username, password string,
) (*config.ServerConfig, resolvedCreds, bool) {
	cur := s.cur()
	_, srv, ok := cur.FindServer(system, server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return nil, resolvedCreds{}, false
	}
	creds, err := s.resolveCreds(username, password, system, server, srv.Username, srv.Password)
	if err != nil {
		writeErr(w, 400, err)
		return nil, resolvedCreds{}, false
	}
	if creds.Password == "" {
		writeErr(w, 400, errors.New("缺少密码（请先在 SSH 终端连接时输入并保存）"))
		return nil, resolvedCreds{}, false
	}
	return srv, creds, true
}

// ============================================================================
// handleSshSftpList — POST /api/ssh/sftp/list
// ============================================================================

func (s *Server) handleSshSftpList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshSftpListReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(req.System) == "" || strings.TrimSpace(req.Server) == "" {
		writeErr(w, 400, errors.New("system / server 不能为空"))
		return
	}
	if req.Path == "" && req.PathID == "" && req.DisplayPath == "" {
		writeErr(w, 400, errors.New("path 不能为空"))
		return
	}
	identity, display, perr := sftpRequestPath(req.PathID, req.DisplayPath, req.Path)
	if perr != nil {
		writeErr(w, 400, perr)
		return
	}

	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	start := time.Now()
	res, err := s.sshSftpListOne(r.Context(), srv, creds.Username, creds.Password, identity, display)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		s.audit.Write("ssh.sftp.list", "system", req.System, "server", req.Server,
			"path", display, "result", "fail", "err", err.Error(), "elapsed_ms", ms)
		writeErrSanitized(w, 502, err)
		return
	}

	cleaned := path.Clean(display)
	parent := ""
	if cleaned != "/" && cleaned != "." {
		parent = path.Dir(cleaned)
	}
	s.audit.Write("ssh.sftp.list", "system", req.System, "server", req.Server,
		"path", cleaned, "result", "ok", "count", len(res.Entries),
		"backend", res.Backend, "elapsed_ms", ms)
	writeJSON(w, 200, sshSftpListResp{
		Path:         cleaned,
		DisplayPath:  cleaned,
		Parent:       parent,
		PathID:       res.DirPathID,
		ParentPathID: res.ParentPathID,
		Entries:      res.Entries,
		Backend:      res.Backend,
		ElapsedMs:    ms,
	})
}

// sshSftpListResult 是 sshSftpListOne 的结果（条目 + 后端名 + 目录身份）。
type sshSftpListResult struct {
	Entries      []sshSftpEntry
	Backend      string
	DirPathID    string
	ParentPathID string
}

// sshSftpEntries 把列目录结果转成响应条目：
// 每条条目都带 path_id（由**已解析的原始父路径**生成，覆盖 ASCII/UTF-8/GBK），
// display_path 是给地址栏/面包屑用的人类可读路径。
func sshSftpEntries(displayParent string, infos []os.FileInfo) []sshSftpEntry {
	entries := make([]sshSftpEntry, 0, len(infos))
	for _, info := range infos {
		display := path.Join(displayParent, info.Name())
		entries = append(entries, sshSftpEntry{
			Name:        info.Name(),
			DisplayPath: display,
			RawName:     sftpclient.RawNameOf(info),
			Encoding:    sftpclient.EncodingOf(info),
			PathID:      sftpclient.PathIDOf(displayParent, info),
			Size:        info.Size(),
			IsDir:       info.IsDir(),
			Mode:        sftpclient.FormatMode(info.Mode()),
			MTime:       info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return entries
}

// sshSftpListOne 实际执行：Dial → NewAuto → ReadDir → 关连接。
// remotePath 是"访问目标"（身份 token 或展示路径），displayPath 用于展示/身份文案。
func (s *Server) sshSftpListOne(
	parentCtx context.Context,
	srv *config.ServerConfig,
	username, password, remotePath, displayPath string,
) (sshSftpListResult, error) {
	ctx, cancel := context.WithTimeout(parentCtx, sshDialOuterTimeout)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: password}, sshAttemptTimeout)
	if err != nil {
		return sshSftpListResult{}, fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer cli.Close()

	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		return sshSftpListResult{}, fmt.Errorf("SFTP 打开失败: %w", err)
	}
	defer sftpCli.Close()

	infos, err := sftpCli.ReadDirCtx(ctx, remotePath)
	if err != nil {
		// sftpclient.ReadDir 自己已经 wrap 过错误，不再重复 wrap
		return sshSftpListResult{Backend: sftpCli.Backend()}, err
	}

	dirID, parentID := sftpDirIdentities(ctx, sftpCli, remotePath)
	if displayPath == "" {
		displayPath = sftpDisplayNameOf(remotePath)
	}
	return sshSftpListResult{
		Entries:      sshSftpEntries(displayPath, infos),
		Backend:      sftpCli.Backend(),
		DirPathID:    dirID,
		ParentPathID: parentID,
	}, nil
}

// ============================================================================
// handleSshSftpPwd — POST /api/ssh/sftp/pwd
// ============================================================================

func (s *Server) handleSshSftpPwd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshSftpPwdReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(req.System) == "" || strings.TrimSpace(req.Server) == "" {
		writeErr(w, 400, errors.New("system / server 不能为空"))
		return
	}

	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), sshSftpPwdTimeout)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: creds.Username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
	if err != nil {
		s.audit.Write("ssh.sftp.pwd", "system", req.System, "server", req.Server,
			"result", "fail", "stage", "dial", "err", err.Error())
		writeErrSanitized(w, 502, err)
		return
	}
	defer cli.Close()

	// pwd 是 POSIX 标准命令，所有 shell（bash/ksh/sh/csh）都支持。
	// encoding 用 utf-8：路径里的中文（少数场景）走 UTF-8 比较稳；
	// 老系统 LANG 未设时会走 C locale，pwd 输出纯 ASCII，无编码问题。
	stdout, _, _, err := cli.Run(ctx, "pwd", 5*time.Second, "utf-8")
	if err != nil {
		s.audit.Write("ssh.sftp.pwd", "system", req.System, "server", req.Server,
			"result", "fail", "stage", "run", "err", err.Error())
		writeErrSanitized(w, 502, fmt.Errorf("执行 pwd 失败: %w", err))
		return
	}
	// pwd 输出形如 "/home/user\n"，trim 掉尾部的换行和空白
	pwd := strings.TrimSpace(stdout)
	if pwd == "" {
		s.audit.Write("ssh.sftp.pwd", "system", req.System, "server", req.Server,
			"result", "fail", "stage", "empty")
		writeErr(w, 500, errors.New("pwd 返回空"))
		return
	}
	// 防御：路径必须以 / 开头（cwd 一定是绝对路径）
	if !strings.HasPrefix(pwd, "/") {
		// 某些 shell 的 pwd 可能输出带 hostname 前缀（极少见），再 trim 一次
		if idx := strings.LastIndex(pwd, "/"); idx >= 0 {
			pwd = pwd[idx:]
		}
	}
	s.audit.Write("ssh.sftp.pwd", "system", req.System, "server", req.Server,
		"result", "ok", "path", pwd)
	writeJSON(w, 200, sshSftpPwdResp{Path: pwd})
}

// ============================================================================
// handleSshSftpPreview — POST /api/ssh/sftp/preview
// ============================================================================

func (s *Server) handleSshSftpPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshSftpPreviewReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(req.System) == "" || strings.TrimSpace(req.Server) == "" {
		writeErr(w, 400, errors.New("system / server 不能为空"))
		return
	}
	if req.Path == "" && req.PathID == "" && req.DisplayPath == "" {
		writeErr(w, 400, errors.New("path 不能为空"))
		return
	}
	previewTarget, previewDisplay, perr := sftpRequestPath(req.PathID, req.DisplayPath, req.Path)
	if perr != nil {
		writeErr(w, 400, perr)
		return
	}

	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = filesPreviewDefaultMax
	}
	if maxBytes > filesPreviewHardMax {
		maxBytes = filesPreviewHardMax
	}
	encoding := normalizePreviewEncoding(req.Encoding)

	raw, fileSize, err := s.sshSftpPreviewOne(r.Context(), srv, creds.Username, creds.Password, previewTarget, maxBytes)
	if err != nil {
		s.audit.Write("ssh.sftp.preview", "system", req.System, "server", req.Server,
			"path", previewDisplay, "result", "fail", "err", err.Error())
		if strings.Contains(err.Error(), "不支持预览目录") {
			writeErr(w, 400, err)
		} else {
			writeErrSanitized(w, 502, err)
		}
		return
	}

	binary := isBinaryBytes(raw)
	var text string
	if binary {
		text = ""
	} else if encoding == "gbk" {
		decoded, derr := simplifiedchinese.GBK.NewDecoder().Bytes(raw)
		if derr != nil {
			s.audit.Write("ssh.sftp.preview", "system", req.System, "server", req.Server,
				"path", previewDisplay, "result", "fail", "stage", "decode", "err", derr.Error())
			writeErr(w, 400, fmt.Errorf("GBK 解码失败（文件可能不是 GBK 编码）: %w", derr))
			return
		}
		text = string(decoded)
	} else {
		text = string(raw)
	}

	resp := filesPreviewResp{
		Name:      path.Base(previewDisplay),
		Path:      path.Clean(previewDisplay),
		Encoding:  encoding,
		Size:      fileSize,
		BytesRead: len(raw),
		Truncated: fileSize > int64(len(raw)),
		IsBinary:  binary,
		Content:   text,
	}
	s.audit.Write("ssh.sftp.preview", "system", req.System, "server", req.Server,
		"path", previewDisplay, "result", "ok", "bytes", len(raw), "encoding", encoding)
	writeJSON(w, 200, resp)
}

// sshSftpPreviewOne Dial → NewAuto → Stat → Open → Read(maxBytes)
// remotePath 是"访问目标"（身份 token 或展示路径），解析阶段带 ctx 可取消。
func (s *Server) sshSftpPreviewOne(
	parentCtx context.Context,
	srv *config.ServerConfig,
	username, password, remotePath string,
	maxBytes int64,
) ([]byte, int64, error) {
	ctx, cancel := context.WithTimeout(parentCtx, sshDialOuterTimeout)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: password}, sshAttemptTimeout)
	if err != nil {
		return nil, 0, fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer cli.Close()

	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		return nil, 0, fmt.Errorf("SFTP 打开失败: %w", err)
	}
	defer sftpCli.Close()

	info, err := sftpCli.StatCtx(ctx, remotePath)
	if err != nil {
		return nil, 0, fmt.Errorf("stat 失败: %w", err)
	}
	if info.IsDir() {
		return nil, 0, fmt.Errorf("不支持预览目录: %s", sftpDisplayNameOf(remotePath))
	}

	f, err := sftpCli.OpenCtx(ctx, remotePath)
	if err != nil {
		return nil, 0, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()

	reader := io.LimitReader(f, maxBytes)
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, info.Size(), fmt.Errorf("读取文件失败: %w", err)
	}
	return raw, info.Size(), nil
}

// ============================================================================
// handleSshSftpDownload — POST /api/ssh/sftp/download
// ============================================================================

func (s *Server) handleSshSftpDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshSftpDownloadReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if len(req.Paths) == 0 {
		writeErr(w, 400, errors.New("paths 不能为空"))
		return
	}
	if len(req.Paths) > sshSftpMaxFilesPerTask {
		writeErr(w, 400, fmt.Errorf("单次最多下载 %d 个文件", sshSftpMaxFilesPerTask))
		return
	}
	if len(req.PathIDs) > 0 && len(req.PathIDs) != len(req.Paths) {
		writeErr(w, 400, errors.New("path_ids 必须与 paths 等长且同序"))
		return
	}
	// BE-020：入口取一次配置快照，整个 handler 复用同一份。
	cur := s.cur()
	// 访问目标：有身份就用身份 token（物理定位），否则回退展示路径。
	// sess.Paths 保存访问目标；展示路径在下载任务里由 sftpDisplayNameOf 还原。
	targets := make([]string, 0, len(req.Paths))
	for i, p := range req.Paths {
		var pathID string
		if len(req.PathIDs) > 0 {
			pathID = req.PathIDs[i]
		}
		// 注意：/api/ssh/sftp/* 命名空间**不校验** app.free_file_roots（本文件头、
		// httpserver.go 路由注释与 handlers_ssh_sftp_upload.go 都明确了这一契约：
		// 用户在 SSH 终端本就能 cd 到任意路径，访问控制由 SSH 账号权限承担）。
		// 这里曾一度加上白名单校验，结果是默认配置 free_file_roots: [] 时
		// 整个 SSH 文件浏览器下载 100% 403，且与同命名空间的 list/preview 行为不一致。
		identity, _, perr := sftpRequestPath(pathID, p, p)
		if perr != nil {
			writeErr(w, 400, perr)
			return
		}
		if identity != p {
			targets = append(targets, identity)
			continue
		}
		targets = append(targets, p)
	}

	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	// target_dir 复用 validateTargetDir 的校验逻辑
	resolvedTarget, err := validateTargetDir(req.TargetDir)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if resolvedTarget != "" && !cur.App.AllowCustomDownloadDirEnabled() {
		writeErr(w, 403, errors.New("自定义下载目录已被配置关闭 (app.allow_custom_download_dir=false)"))
		return
	}
	if resolvedTarget != "" && !cur.App.TargetDirAllowed(resolvedTarget) {
		writeErr(w, 403, fmt.Errorf("target_dir %q 不在 app.allowed_download_roots 白名单中", resolvedTarget))
		return
	}
	downloadRoot := resolvedTarget
	if downloadRoot == "" {
		downloadRoot = cur.DownloadDir()
	}

	id := dlmanager.NewID()
	sessCtx, cancel := context.WithCancel(context.Background())
	sess := &dlmanager.Session{
		ID:        id,
		Kind:      "ssh-sftp",
		System:    req.System,
		Server:    req.Server,
		Paths:     targets,
		Zip:       req.Zip,
		Folder:    downloadRoot,
		CreatedAt: time.Now(),
	}
	sess.AttachCancel(cancel)
	s.downloads.Create(sess)
	go s.downloads.IdleGC(sess)

	s.audit.Write("ssh.sftp.download.start", "system", req.System, "server", req.Server,
		"files", len(req.Paths), "target_dir", downloadRoot, "zip", req.Zip)
	writeJSON(w, 200, map[string]any{
		"id":         id,
		"target_dir": downloadRoot,
	})

	go s.runSshSftpDownloadTask(sessCtx, sess, srv, creds.Username, creds.Password)
}

// runSshSftpDownloadTask 后台执行下载任务。
// 复用 dlmanager.Session 做进度广播，复用 sftpclient.NewAuto 做 SFTP/shell 兜底。
func (s *Server) runSshSftpDownloadTask(
	ctx context.Context,
	sess *dlmanager.Session,
	srv *config.ServerConfig,
	username, password string,
) {
	dialCtx, cancelDial := context.WithTimeout(ctx, sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: password}, sshAttemptTimeout)
	cancelDial()
	if err != nil {
		s.audit.Write("ssh.sftp.download", "system", sess.System, "server", sess.Server,
			"result", "fail", "stage", "dial", "err", err.Error())
		sess.MarkFinished(nil, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
	defer cli.Close()

	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		sess.MarkFinished(nil, fmt.Errorf("SFTP 打开失败: %w", err))
		return
	}
	defer sftpCli.Close()

	// 取消打断：ctx 被 cancel 时主动关连接
	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = sftpCli.Close()
			_ = cli.Close()
		case <-cancelDone:
		}
	}()
	defer close(cancelDone)

	// 复用 downloadSeriesFree 逻辑：串行下多个文件，进度广播。
	// sftpCli 是 *sftpclient.Client，已实现 sftpClientLike 接口
	// （Close/ReadDir/Stat/DownloadFile/DownloadFileWithProgress/Open），直接传入即可。
	// v1.4：返回 hadDir，选中目录时强制打 zip 保留目录结构。
	results, hadDir, err := s.downloadSeriesFree(ctx, srv, sess.Paths, sftpCli, sess)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.audit.Write("ssh.sftp.download", "system", sess.System, "server", sess.Server,
				"result", "fail", "stage", "cancel")
			sess.MarkFinished(results, fmt.Errorf("已取消"))
			return
		}
		s.audit.Write("ssh.sftp.download", "system", sess.System, "server", sess.Server,
			"result", "fail", "stage", "download", "err", err.Error())
		sess.MarkFinished(results, err)
		return
	}

	// 可选 zip：多文件（>= 2）才打；选中目录（hadDir）时强制打 zip 保留目录结构
	zipWanted := (sess.Zip && len(results) >= 2) || hadDir
	if zipWanted && len(results) > 0 {
		zipName := fmt.Sprintf("%s_files_%s.zip", sanitize(srv.Name), results[0].Date)
		zipPath := filepath.Join(sess.Folder, results[0].Date, zipName)
		sources := make([]ZipSource, 0, len(results))
		remoteNames := make([]string, 0, len(results))
		for _, it := range results {
			p := filepath.Join(sess.Folder, it.Date, it.Local)
			nameInZip := it.ZipName
			if nameInZip == "" {
				nameInZip = path.Base(it.Remote)
			}
			sources = append(sources, ZipSource{Path: p, NameInZip: nameInZip})
			remoteNames = append(remoteNames, it.Remote)
		}
		if err := zipFilesNamed(sources, zipPath); err != nil {
			s.audit.Write("ssh.sftp.download", "system", sess.System, "server", sess.Server,
				"result", "fail", "stage", "zip", "err", err.Error())
			sess.MarkFinished(results, fmt.Errorf("打包 zip 失败: %w", err))
			return
		}
		st, _ := os.Stat(zipPath)
		var size int64
		if st != nil {
			size = st.Size()
		}
		results = append(results, dlmanager.Item{
			Local:   zipName,
			Bytes:   strconv.FormatInt(size, 10),
			Date:    results[0].Date,
			Kind:    "zip",
			AbsPath: zipPath,
		})
		_ = downloads.WriteMeta(zipPath, downloads.Meta{
			System: sess.System,
			Server: srv.Name,
			Host:   fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			Files:  remoteNames,
			Kind:   "zip",
		})
	}

	s.audit.Write("ssh.sftp.download", "system", sess.System, "server", sess.Server,
		"result", "ok", "files", len(results), "selected", len(sess.Paths))
	sess.MarkFinished(results, nil)
	go s.TriggerCleanup()
}

// ============================================================================
// handleSshSftpDownloadEventsOrCancel — GET/POST /api/ssh/sftp/download/{id}/events|cancel
// ============================================================================

func (s *Server) handleSshSftpDownloadEventsOrCancel(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/ssh/sftp/download/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	action := parts[1]
	switch action {
	case "events":
		s.streamDownloadEvents(w, r, id)
	case "cancel":
		s.cancelDownload(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

// ============================================================================
// handleSshSftpMkdir / handleSshSftpCreate / handleSshSftpRename
// ============================================================================

type sshSftpMkdirReq struct {
	System        string `json:"system"`
	Server        string `json:"server"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	Path          string `json:"path"` // 旧契约：展示绝对路径
	ParentPathID  string `json:"parent_path_id,omitempty"`
	Name          string `json:"name,omitempty"`
}

type sshSftpCreateReq struct {
	System       string `json:"system"`
	Server       string `json:"server"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	Path         string `json:"path"` // 旧契约：展示绝对路径
	ParentPathID string `json:"parent_path_id,omitempty"`
	Name         string `json:"name,omitempty"`
}

type sshSftpRenameReq struct {
	System           string `json:"system"`
	Server           string `json:"server"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	OldPath          string `json:"old_path"`
	NewPath          string `json:"new_path"`
	OldPathID        string `json:"old_path_id,omitempty"`
	NewParentPathID  string `json:"new_parent_path_id,omitempty"`
	NewName          string `json:"new_name,omitempty"`
}

func (s *Server) handleSshSftpMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req sshSftpMkdirReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体 JSON 格式错误: %w", err))
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	target, display, terr := sftpCreateTarget(req.ParentPathID, req.Name, "", req.Path)
	if terr != nil {
		writeErr(w, 400, terr)
		return
	}
	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	dialCtx, dialCancel := context.WithTimeout(r.Context(), sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: creds.Username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
	dialCancel()
	if err != nil {
		writeErrSanitized(w, 502, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
	defer cli.Close()

	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		writeErrSanitized(w, 502, fmt.Errorf("SFTP 打开失败: %w", err))
		return
	}
	defer sftpCli.Close()

	if err := sftpCli.MkdirAllCtx(r.Context(), target); err != nil {
		writeErrSanitized(w, 500, fmt.Errorf("新建文件夹失败: %w", err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "path": display})
}

func (s *Server) handleSshSftpCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req sshSftpCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体 JSON 格式错误: %w", err))
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	target, display, terr := sftpCreateTarget(req.ParentPathID, req.Name, "", req.Path)
	if terr != nil {
		writeErr(w, 400, terr)
		return
	}
	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	dialCtx, dialCancel := context.WithTimeout(r.Context(), sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: creds.Username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
	dialCancel()
	if err != nil {
		writeErrSanitized(w, 502, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
	defer cli.Close()

	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		writeErrSanitized(w, 502, fmt.Errorf("SFTP 打开失败: %w", err))
		return
	}
	defer sftpCli.Close()

	if err := sftpCli.CreateExclusive(r.Context(), target); err != nil {
		writeErrSanitized(w, 500, fmt.Errorf("新建文件失败: %w", err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "path": display})
}

func (s *Server) handleSshSftpRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req sshSftpRenameReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体 JSON 格式错误: %w", err))
		return
	}
	req.OldPath = strings.TrimSpace(req.OldPath)
	req.NewPath = strings.TrimSpace(req.NewPath)
	// 源：优先 old_path_id（身份），回退 old_path（展示路径）。
	oldTarget, oldDisplay, oerr := sftpRequestPath(req.OldPathID, "", req.OldPath)
	if oerr != nil {
		writeErr(w, 400, oerr)
		return
	}
	// 目的：优先 (new_parent_path_id + new_name)，回退 new_path。
	newTarget, newDisplay, nerr := sftpCreateTarget(req.NewParentPathID, req.NewName, "", req.NewPath)
	if nerr != nil {
		writeErr(w, 400, nerr)
		return
	}
	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	dialCtx, dialCancel := context.WithTimeout(r.Context(), sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: creds.Username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
	dialCancel()
	if err != nil {
		writeErrSanitized(w, 502, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
	defer cli.Close()

	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		writeErrSanitized(w, 502, fmt.Errorf("SFTP 打开失败: %w", err))
		return
	}
	defer sftpCli.Close()

	if err := sftpCli.Rename(oldTarget, newTarget); err != nil {
		writeErrSanitized(w, 500, fmt.Errorf("重命名失败: %w", err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "old_path": oldDisplay, "new_path": newDisplay})
}
