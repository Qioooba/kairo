package httpserver

// handlers_ssh_sftp_upload.go — SSH/SFTP 文件上传 handler（v1.1 新增）。
//
// 设计原则（详见评估方案）：
//   - 走 /api/ssh/sftp/upload/* 命名空间，与 edit 一致，不校验 free_file_roots
//     （访问控制由 SSH 账号权限承担）；
//   - 流式 io.Copy，单 POST 整文件，不引入 chunk/续传/tus；
//   - partial + Rename 原子落盘（partial 名 ".<filename>.partial"）；
//   - 进度用 XHR.upload.onprogress（前端），后端只在终态推一次 audit；
//   - 复用 resolveSshSftpCreds + dlmanager.Session（仅用 cancel 能力，不上 SSE）；
//   - 复用 sftpclient.NewAuto（SFTP + shell 兜底）；
//   - 单文件大小校验 app.upload_max_size（默认 2GB）；
//   - 覆盖策略三档：reject（默认）/ replace / rename（追加 _1/_2 后缀）。
//
// 三个端点：
//   POST /api/ssh/sftp/upload/init    → 建会话、校验、返回 {id}
//   POST /api/ssh/sftp/upload/{id}/data → 流式接收文件内容，写到 partial，rename
//   POST /api/ssh/sftp/upload/cancel  → 取消（与下载的 cancel 复用形态）

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"kairo/internal/config"
	"kairo/internal/dlmanager"
	"kairo/internal/sftpclient"
	"kairo/internal/sshclient"
)

// ============================================================================
// 请求/响应类型
// ============================================================================

// sshSftpUploadInitReq 上传初始化请求
type sshSftpUploadInitReq struct {
	System    string `json:"system"`
	Server    string `json:"server"`
	Username  string `json:"username"`   // 可选；空则用 server 配置默认
	Password  string `json:"password"`   // 可选；空则走 resolveCreds
	TargetDir string `json:"target_dir"` // 远端目标目录，绝对路径
	Filename  string `json:"filename"`   // 文件名（不含路径）
	Size      int64  `json:"size"`       // 文件总字节数（用于校验）
	Overwrite string `json:"overwrite"` // "reject"(默认) | "replace" | "rename"
}

// sshSftpUploadInitResp 上传初始化响应
type sshSftpUploadInitResp struct {
	ID          string `json:"id"`
	PartialPath string `json:"partial_path"` // .partial 完整路径（信息用）
	FinalName   string `json:"final_name"`   // 最终落盘文件名（rename 策略可能改变）
	MaxSize     int64  `json:"max_size"`     // 服务端允许的最大字节数
}

// sshSftpUploadDataResp 上传数据响应
type sshSftpUploadDataResp struct {
	OK         bool   `json:"ok"`
	Bytes      int64  `json:"bytes"`
	RemotePath string `json:"remote_path"`
	FinalName  string `json:"final_name"`
	Error      string `json:"error,omitempty"`
}

// ============================================================================
// 常量
// ============================================================================

const (
	// sshSftpUploadInitBodyLimit init 请求体上限（小 JSON）
	sshSftpUploadInitBodyLimit = 64 * 1024
	// sshSftpUploadIdleTimeout 上传 session 空闲超时（30 分钟，与下载一致）
	// 超过这个时间还没收到 /data 请求，session 被 idleGC 回收
	sshSftpUploadIdleTimeout = 30 * time.Minute
)

// ============================================================================
// uploadState — init 后到 /data 之间的会话状态
// ============================================================================

// uploadState 保存 init 阶段解析出的会话上下文，/data 时取出复用。
//
// 不复用 dlmanager.Session：dlmanager.Session 是下载场景设计的（Paths、Folder、
// 广播 channel），上传场景只需要 cancel 能力 + 状态透传。
// 这里用一个独立的 map 存 uploadState，cancel 走自己的 cancel func。
type uploadState struct {
	ID          string
	System      string
	Server      string
	Srv         *config.ServerConfig
	Creds       resolvedCreds
	TargetDir   string // 已校验的绝对路径
	Filename    string // 已校验的文件名（basename）
	FinalName   string // 最终落盘文件名（rename 策略可能改）
	PartialPath string // target_dir/.<filename>.partial
	Overwrite   string
	Size        int64
	MaxSize     int64
	CreatedAt   time.Time
	ctx         context.Context
	cancel      context.CancelFunc
}

// uploadStates 存放活跃的 upload 会话。key = uploadState.ID
// 使用 uploadStateMap 内部的 sync.RWMutex 保护（与 downloads 分离，避免锁竞争）。
//
// 注意：必须独立于 s.downloads，因为：
//   - 上传 session 不需要 SSE 订阅
//   - IdleGC 频率不同
//   - 避免与下载 session 互相 cancel 时的混淆
type uploadStateMap struct {
	mu    sync.RWMutex
	items map[string]*uploadState
}

func newUploadStateMap() *uploadStateMap {
	return &uploadStateMap{items: make(map[string]*uploadState)}
}

func (m *uploadStateMap) Get(id string) (*uploadState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.items[id]
	return s, ok
}

func (m *uploadStateMap) Put(s *uploadState) {
	m.mu.Lock()
	m.items[s.ID] = s
	m.mu.Unlock()
}

func (m *uploadStateMap) Delete(id string) {
	m.mu.Lock()
	delete(m.items, id)
	m.mu.Unlock()
}

// ============================================================================
// handleSshSftpUploadInit — POST /api/ssh/sftp/upload/init
// ============================================================================

func (s *Server) handleSshSftpUploadInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshSftpUploadInitReq
	if err := json.NewDecoder(io.LimitReader(r.Body, sshSftpUploadInitBodyLimit)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	if strings.TrimSpace(req.System) == "" || strings.TrimSpace(req.Server) == "" {
		writeErr(w, 400, errors.New("system / server 不能为空"))
		return
	}
	if strings.TrimSpace(req.TargetDir) == "" {
		writeErr(w, 400, errors.New("target_dir 不能为空"))
		return
	}
	if strings.TrimSpace(req.Filename) == "" {
		writeErr(w, 400, errors.New("filename 不能为空"))
		return
	}

	// 路径安全校验
	targetDir, fname, err := validateUploadPath(req.TargetDir, req.Filename)
	if err != nil {
		writeErr(w, 400, err)
		return
	}

	// 文件大小校验
	cur := s.cur()
	maxSize := cur.App.UploadMaxSizeBytes()
	if req.Size <= 0 {
		writeErr(w, 400, errors.New("size 必须大于 0"))
		return
	}
	if maxSize > 0 && req.Size > maxSize {
		writeErr(w, 400, fmt.Errorf("文件大小 %d 超过上限 %d（app.upload_max_size）", req.Size, maxSize))
		return
	}

	// 覆盖策略归一
	overwrite := strings.ToLower(strings.TrimSpace(req.Overwrite))
	if overwrite == "" {
		overwrite = "reject"
	}
	if overwrite != "reject" && overwrite != "replace" && overwrite != "rename" {
		writeErr(w, 400, fmt.Errorf("overwrite 必须是 reject/replace/rename, got %q", overwrite))
		return
	}

	// 凭据解析（不在此 Dial，/data 时再 Dial，避免 init 后长时间空占连接）
	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	// 决定最终文件名（rename 策略需要先 Dial 列目录找候选名）
	// 但 init 阶段 Dial 代价大；折中：rename 策略在 /data 阶段再实际计算
	// 这里先用原 filename 占位
	finalName := fname
	partialPath := path.Join(targetDir, "."+fname+".partial")

	// init 阶段如果 overwrite=="reject"，需要检查目标是否已存在
	// 这要 Dial 一次。为避免 init 太重，把这个检查也推到 /data 阶段
	// （reject 策略下，/data 阶段先 Stat，已存在就 400）

	// 建会话
	id := dlmanager.NewID() // 复用 ID 生成器（带 dl- 前缀，便于排查）
	// 这里用 "up-" 前缀覆盖，区分上传/下载
	id = "up-" + id[3:]
	// state.ctx 是一个独立的 ctx，与 HTTP 请求解耦
	// cancel 端点触发 state.cancel 后，所有派生 ctx（包括 /data 里的）都会 Done
	stateCtx, stateCancel := context.WithCancel(context.Background())
	state := &uploadState{
		ID:          id,
		System:      req.System,
		Server:      req.Server,
		Srv:         srv,
		Creds:       creds,
		TargetDir:   targetDir,
		Filename:    fname,
		FinalName:   finalName,
		PartialPath: partialPath,
		Overwrite:   overwrite,
		Size:        req.Size,
		MaxSize:     maxSize,
		CreatedAt:   time.Now(),
		ctx:         stateCtx,
		cancel:      stateCancel,
	}
	s.uploadStates.Put(state)

	// idle GC：init 后超过 sshSftpUploadIdleTimeout 仍无 /data 请求 → 自动清理
	// state.ctx 被 cancel（/data 成功 / cancel 端点 / /data 失败）时 goroutine 退出
	go func() {
		timer := time.NewTimer(sshSftpUploadIdleTimeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			if _, ok := s.uploadStates.Get(id); ok {
				state.cancel()
				s.uploadStates.Delete(id)
				s.audit.Write("ssh.sftp.upload.idle_timeout", "upload_id", id,
					"system", req.System, "server", req.Server)
			}
		case <-state.ctx.Done():
			// state 已被消费或取消，无需 idle GC
		}
	}()

	s.audit.Write("ssh.sftp.upload.init", "system", req.System, "server", req.Server,
		"target_dir", targetDir, "filename", fname, "size", req.Size, "overwrite", overwrite,
		"upload_id", id)

	writeJSON(w, 200, sshSftpUploadInitResp{
		ID:          id,
		PartialPath: partialPath,
		FinalName:   finalName,
		MaxSize:     maxSize,
	})
}

// ============================================================================
// handleSshSftpUploadData — POST /api/ssh/sftp/upload/{id}/data
// ============================================================================

func (s *Server) handleSshSftpUploadData(w http.ResponseWriter, r *http.Request) {
	// P0-BUG 修复：上传大文件或网络异常时底层 sftp 库可能 panic，
	// 不加 recover 会导致整个进程崩溃（Win7 上尤为明显）。
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[upload] panic recovered in handleSshSftpUploadData: %v", rec)
			s.audit.Write("ssh.sftp.upload", "result", "panic", "err", fmt.Sprintf("%v", rec))
			writeErr(w, 500, errors.New("内部错误：上传处理异常"))
		}
	}()
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	// 从 URL 取 id：/api/ssh/sftp/upload/{id}/data
	// 用 strings.TrimPrefix + 去掉 /data 后缀
	urlPath := strings.TrimPrefix(r.URL.Path, "/api/ssh/sftp/upload/")
	if !strings.HasSuffix(urlPath, "/data") {
		writeErr(w, 404, errors.New("路径不存在"))
		return
	}
	id := strings.TrimSuffix(urlPath, "/data")
	if id == "" {
		writeErr(w, 400, errors.New("缺少 upload id"))
		return
	}

	state, ok := s.uploadStates.Get(id)
	if !ok {
		writeErr(w, 404, errors.New("上传会话不存在或已过期"))
		return
	}

	// 校验 Content-Length 与 init 的 size 一致
	// r.ContentLength 在客户端发了 Content-Length 时为正；-1 表示未知（chunked）
	// 严格模式：要求客户端必须发 Content-Length，且必须等于 init 声明的 size
	if r.ContentLength < 0 {
		writeErr(w, 400, errors.New("缺少 Content-Length（客户端必须声明文件大小）"))
		s.uploadStates.Delete(id)
		state.cancel()
		return
	}
	if r.ContentLength != state.Size {
		writeErr(w, 400, fmt.Errorf("Content-Length %d 与 init 声明 %d 不符", r.ContentLength, state.Size))
		s.uploadStates.Delete(id)
		state.cancel()
		return
	}

	// Dial SSH + SFTP
	// 合并两个 ctx：r.Context()（HTTP 客户端断开时 Done）+ state.ctx（cancel 端点触发时 Done）
	// 用 context.WithCancel 从 state.ctx 派生，r.Context() Done 时也 cancel
	ctx, cancel := context.WithCancel(state.ctx)
	defer cancel()
	go func() {
		select {
		case <-r.Context().Done():
			cancel()
		case <-ctx.Done():
		}
	}()

	dialCtx, dialCancel := context.WithTimeout(ctx, sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: state.Srv.Name, Host: state.Srv.Host, Port: state.Srv.Port, Username: state.Creds.Username,
		HostKeySHA256: state.Srv.HostKeySHA256, SSHProfile: state.Srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: state.Creds.Password}, sshAttemptTimeout)
	dialCancel()
	if err != nil {
		s.audit.Write("ssh.sftp.upload", "system", state.System, "server", state.Server,
			"result", "fail", "stage", "dial", "upload_id", id, "err", err.Error())
		writeErrSanitized(w, 502, fmt.Errorf("SSH 连接失败: %w", err))
		s.uploadStates.Delete(id)
		state.cancel()
		return
	}
	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		s.audit.Write("ssh.sftp.upload", "system", state.System, "server", state.Server,
			"result", "fail", "stage", "sftp", "upload_id", id, "err", err.Error())
		writeErrSanitized(w, 502, fmt.Errorf("SFTP 打开失败: %w", err))
		s.uploadStates.Delete(id)
		state.cancel()
		return
	}

	// 用 sync.Once 保护 Close，避免 goroutine 和 defer 并发调用导致 panic
	var closeOnce sync.Once
	closeAll := func() {
		_ = sftpCli.Close()
		_ = cli.Close()
	}
	defer closeOnce.Do(closeAll)

	// 取消打断：ctx 被 cancel 时主动关连接
	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			closeOnce.Do(closeAll)
		case <-cancelDone:
		}
	}()
	defer close(cancelDone)

	// 覆盖策略处理：在写之前确定 finalName
	finalName := state.Filename
	finalPath := path.Join(state.TargetDir, finalName)

	// Stat 现有目标
	_, statErr := sftpCli.Stat(finalPath)
	exists := statErr == nil

	switch state.Overwrite {
	case "reject":
		if exists {
			s.audit.Write("ssh.sftp.upload", "system", state.System, "server", state.Server,
				"result", "fail", "stage", "exists", "upload_id", id, "path", finalPath)
			writeErr(w, 409, fmt.Errorf("目标已存在: %s（覆盖策略=拒绝）", finalPath))
			s.uploadStates.Delete(id)
			state.cancel()
			return
		}
	case "replace":
		// 落盘阶段用"备份-替换-清理"流程，见下方
	case "rename":
		if exists {
			// 找一个不冲突的名字：name_1.ext / name_2.ext / ...
			finalName, err = pickRenameCandidate(sftpCli, state.TargetDir, state.Filename)
			if err != nil {
				writeErr(w, 500, err)
				s.uploadStates.Delete(id)
				state.cancel()
				return
			}
			finalPath = path.Join(state.TargetDir, finalName)
		}
	}

	// 流式上传到 partial
	// 注意：partial 文件名要反映 finalName，避免 rename 时撞名
	partialPath := path.Join(state.TargetDir, "."+finalName+".partial")

	// 如果同名 partial 已存在（上次上传失败残留），先删除
	// （Create 是 truncate 语义，理论上会覆盖，但显式删更干净）
	if _, err := sftpCli.Stat(partialPath); err == nil {
		_ = sftpCli.Remove(partialPath)
	}

	// 进度回调（推 SSE 的话接 sess.BroadcastEvent；这里仅 audit 最终结果，不推中间事件）
	// 进度由前端 XHR.upload.onprogress 自己算，后端不需要实时推送
	progress := func(written, total int64) {
		// 预留：如要接 SSE，在这里 sess.BroadcastEvent("progress", ...)
		// 当前实现：no-op，节省后端开销
		_ = written
		_ = total
	}

	err = sftpCli.UploadStream(ctx, r.Body, partialPath, 0o644, progress)
	if err != nil {
		// 上传失败：删 partial，避免残留
		_ = sftpCli.Remove(partialPath)
		s.audit.Write("ssh.sftp.upload", "system", state.System, "server", state.Server,
			"result", "fail", "stage", "upload", "upload_id", id,
			"path", finalPath, "size", state.Size, "err", err.Error())
		// 区分取消与失败
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeErrSanitized(w, 499, fmt.Errorf("上传已取消: %w", err))
		} else {
			writeErrSanitized(w, 502, fmt.Errorf("上传失败: %w", err))
		}
		s.uploadStates.Delete(id)
		state.cancel()
		return
	}

	// 落盘：partial → final，按 overwrite 策略处理
	// replace 模式 + 目标已存在：用"备份-替换-清理"流程，避免 Rename 失败时原文件丢失
	// reject / rename 模式：目标不存在（reject 已校验；rename 已选不冲突名），直接 Rename
	if state.Overwrite == "replace" && exists {
		backupPath := finalPath + ".replace-bak-" + id
		if err := sftpCli.Rename(finalPath, backupPath); err != nil {
			_ = sftpCli.Remove(partialPath)
			s.audit.Write("ssh.sftp.upload", "system", state.System, "server", state.Server,
				"result", "fail", "stage", "backup", "upload_id", id,
				"path", finalPath, "err", err.Error())
			writeErrSanitized(w, 502, fmt.Errorf("备份原文件失败，已取消上传: %w", err))
			s.uploadStates.Delete(id)
			state.cancel()
			return
		}
		if err := sftpCli.Rename(partialPath, finalPath); err != nil {
			// rename 失败：恢复备份，删 partial
			_ = sftpCli.Rename(backupPath, finalPath)
			_ = sftpCli.Remove(partialPath)
			s.audit.Write("ssh.sftp.upload", "system", state.System, "server", state.Server,
				"result", "fail", "stage", "rename", "upload_id", id,
				"path", finalPath, "err", err.Error())
			writeErrSanitized(w, 502, fmt.Errorf("覆盖失败（原文件已恢复）: %w", err))
			s.uploadStates.Delete(id)
			state.cancel()
			return
		}
		// 成功：删备份。Remove 失败不致命（备份文件残留不影响数据正确性），
		// 但必须写 audit 让用户知道远端有残留备份文件需要手动清理。
		if err := sftpCli.Remove(backupPath); err != nil {
			s.audit.Write("ssh.sftp.upload", "system", state.System, "server", state.Server,
				"result", "warn", "stage", "cleanup_backup", "upload_id", id,
				"path", finalPath, "backup_path", backupPath, "err", err.Error())
		}
	} else {
		if err := sftpCli.Rename(partialPath, finalPath); err != nil {
			_ = sftpCli.Remove(partialPath)
			s.audit.Write("ssh.sftp.upload", "system", state.System, "server", state.Server,
				"result", "fail", "stage", "rename", "upload_id", id,
				"path", finalPath, "err", err.Error())
			writeErrSanitized(w, 502, fmt.Errorf("重命名失败: %w", err))
			s.uploadStates.Delete(id)
			state.cancel()
			return
		}
	}

	// 成功收尾
	s.uploadStates.Delete(id)
	state.cancel() // 通知 idle GC goroutine 退出
	s.audit.Write("ssh.sftp.upload", "system", state.System, "server", state.Server,
		"result", "ok", "upload_id", id, "path", finalPath,
		"size", state.Size, "bytes", state.Size)

	writeJSON(w, 200, sshSftpUploadDataResp{
		OK:         true,
		Bytes:      state.Size,
		RemotePath: finalPath,
		FinalName:  finalName,
	})
}

// ============================================================================
// handleSshSftpUploadCancel — POST /api/ssh/sftp/upload/cancel
// ============================================================================

func (s *Server) handleSshSftpUploadCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	if req.ID == "" {
		writeErr(w, 400, errors.New("id 不能为空"))
		return
	}

	state, ok := s.uploadStates.Get(req.ID)
	if !ok {
		// 幂等：会话已结束（可能 /data 已正常退出并清理了 state），
		// cancel 的语义是"确保终止"，已终止也返回成功。
		writeJSON(w, 200, map[string]any{"ok": true, "already_done": true})
		return
	}
	// 触发 cancel：让正在进行的 /data handler 的 ctx.Done
	if state.cancel != nil {
		state.cancel()
	}
	s.audit.Write("ssh.sftp.upload.cancel", "upload_id", req.ID,
		"system", state.System, "server", state.Server)
	// 不在这里 Delete：/data handler 收到 ctx.Done 后会自己删 partial + 退出
	// 但如果 cancel 时 /data 还没开始（极少见），需要兜底清理
	// 等 state.ctx.Done() 后立即删（/data 正常退出路径会触发），最多等 10 秒兜底
	go func() {
		select {
		case <-state.ctx.Done():
			// /data handler 正常退出或 cancel 立即生效
		case <-time.After(10 * time.Second):
			// 兜底：10 秒还没 Done，强制清理
		}
		s.uploadStates.Delete(req.ID)
	}()
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ============================================================================
// helpers
// ============================================================================

// validateUploadPath 校验并归一 target_dir 和 filename。
//
// 校验规则：
//   - target_dir 必须是绝对路径（以 / 开头）
//   - target_dir 不能含控制字符（NUL / \n / \r）
//   - target_dir path.Clean 后必须与原串一致（防 .. / 多斜杠注入）
//   - filename 必须是 basename（无 / \ 路径分隔符）
//   - filename 不能是 "." ".." 或含路径分隔符
//   - filename 不能含控制字符
//
// 返回归一后的 (targetDir, filename, err)
func validateUploadPath(targetDir, filename string) (string, string, error) {
	// 先在原始输入上检测控制字符（必须在 TrimSpace 之前，否则 \n\r\x00 会被
	// 当作空白削掉从而绕过校验——可注入到日志 / shell 命令）。
	if strings.ContainsAny(targetDir, "\x00\n\r") {
		return "", "", errors.New("target_dir 含非法字符")
	}
	if strings.ContainsAny(filename, "\x00\n\r") {
		return "", "", errors.New("filename 含非法字符")
	}
	// targetDir
	td := strings.TrimSpace(targetDir)
	if !strings.HasPrefix(td, "/") {
		return "", "", fmt.Errorf("target_dir 必须是绝对路径: %q", td)
	}
	// 拒绝含 ".." 的路径（不静默归一化）：用户传 /tmp/../etc 期望 400，
	// 而不是被静默写到 /etc。访问控制由 SSH 账号权限承担，这里只做基本格式校验。
	if strings.Contains(td, "..") {
		return "", "", fmt.Errorf("target_dir 含 \"..\" 路径段: %q", td)
	}
	// 末尾斜杠 / 冗余分隔符（//）可被 path.Clean 安全归一化
	td = path.Clean(td)
	// 防御：clean 后不能是 "." 或 ".."（绝对路径不会，但兜底）
	if td == "." || td == ".." {
		return "", "", fmt.Errorf("target_dir 非法: %q", td)
	}

	// filename
	fn := strings.TrimSpace(filename)
	if fn == "" {
		return "", "", errors.New("filename 不能为空")
	}
	if fn == "." || fn == ".." {
		return "", "", errors.New("filename 不能是 . 或 ..")
	}
	if strings.ContainsAny(fn, "/\\") {
		return "", "", fmt.Errorf("filename 不能含路径分隔符: %q", fn)
	}
	// basename 兜底（防 filename 含 "../../etc/passwd" 等）
	if path.Base(fn) != fn {
		return "", "", fmt.Errorf("filename 不是合法 basename: %q", fn)
	}
	return td, fn, nil
}

// statChecker 是 pickRenameCandidate 需要的最小依赖（便于单测注入 mock）。
// *sftpclient.Client 天然满足此接口。
type statChecker interface {
	Stat(path string) (os.FileInfo, error)
}

// pickRenameCandidate 在 targetDir 下找一个不冲突的文件名。
//
// 策略：name_1.ext / name_2.ext / ... 直到 99 次。超过返回错误。
func pickRenameCandidate(sftpCli statChecker, targetDir, originalName string) (string, error) {
	// 拆 ext：name + ".ext"
	// idx > 0 保证 ".bashrc" 这种 dotfile 不被拆成 "" + ".bashrc"
	var name, ext string
	if idx := strings.LastIndex(originalName, "."); idx > 0 {
		name = originalName[:idx]
		ext = originalName[idx:]
	}
	if name == "" {
		name = originalName
		ext = ""
	}
	for i := 1; i <= 99; i++ {
		candidate := fmt.Sprintf("%s_%d%s", name, i, ext)
		candidatePath := path.Join(targetDir, candidate)
		_, err := sftpCli.Stat(candidatePath)
		if err == nil {
			// 已存在，继续试下一个
			continue
		}
		// 精确判断"不存在"：fs.ErrNotExist 能匹配 sftp.ErrSshFxNoSuchFile
		// （pkg/sftp 的 StatusError.Unwrap 返回 fs.ErrNotExist）。
		// 其他错误（如权限不足）不能当作"可用"，否则后续 Rename 会覆盖已存在文件。
		if errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
		return "", fmt.Errorf("检查候选名 %q 时出错: %w", candidate, err)
	}
	return "", fmt.Errorf("找不到可用重命名候选（已试 %s_1 ~ %s_99）", name, name)
}
