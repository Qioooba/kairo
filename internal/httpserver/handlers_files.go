package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ops-toolbox/internal/config"
	"ops-toolbox/internal/dlmanager"
	"ops-toolbox/internal/downloads"
	"ops-toolbox/internal/sftpclient"
	"ops-toolbox/internal/sshclient"
)

// sftpDialer 把 SSH 连接变成 SFTP 客户端。
//
// 生产 = sftpclient.New；测试可替换。
// 用包级变量而不是 Server 字段，是为了让 handler 直接拿到（不需要传遍所有调用点），
// 且 override 仅在测试代码里发生。
var sftpDialer = func(cli *sshclient.Client) (sftpClientLike, error) {
	return sftpclient.New(cli.RawConn())
}

// sftpClientLike 是 *sftpclient.Client 的最小接口（让 handler 不直接依赖具体类型，
// 便于测试替换）。
type sftpClientLike interface {
	Close() error
	ReadDir(path string) ([]os.FileInfo, error)
	DownloadFile(remotePath, localPath string) (int64, error)
	DownloadFileWithProgress(remotePath, localPath string, progress func(written, total int64)) (int64, error)
}

// ============================================================================
// v0.3：文件浏览器（任意路径浏览 + 下载，按 SSH 账号权限放行）
// ============================================================================

// filesListReq 列目录请求体
type filesListReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
	Path     string `json:"path"` // 必须以 "/" 开头的绝对路径
}

// filesEntry 目录条目（用于前端表格）
type filesEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
	Mode  string `json:"mode"`  // 例如 "drwxr-xr-x"，便于 UI 显示
	MTime string `json:"mtime"` // RFC3339
}

// handleFilesList 列远端目录（任意路径）。
//
// 与 /api/logs/list 的差别：
//   - 不做白名单校验，按用户 SSH 账号的实际权限放行（v0.3 自由模式）；
//   - 返回完整目录条目（含 size/mode/mtime），UI 可直接做表格；
//   - parent 字段给出"上一级"绝对路径，没有则 null（根目录就是 null）。
func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req filesListReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if req.Path == "" {
		writeErr(w, 400, errors.New("path 不能为空"))
		return
	}
	if !strings.HasPrefix(req.Path, "/") {
		writeErr(w, 400, errors.New("path 必须是绝对路径（以 / 开头）"))
		return
	}
	_, srv, ok := s.cur().FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
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

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: creds.Username,
	}, sshclient.Credentials{Password: creds.Password}, 10*time.Second)
	if err != nil {
		s.audit.Write("files.list", "system", req.System, "server", req.Server, "path", req.Path, "result", "fail", "stage", "dial", "err", err.Error())
		writeErrSanitized(w, 502, err)
		return
	}
	defer cli.Close()

	sftpCli, err := sftpDialer(cli)
	if err != nil {
		writeErrSanitized(w, 502, err)
		return
	}
	defer sftpCli.Close()

	infos, err := sftpCli.ReadDir(req.Path)
	if err != nil {
		s.audit.Write("files.list", "system", req.System, "server", req.Server, "path", req.Path, "result", "fail", "err", err.Error())
		writeErrSanitized(w, 502, fmt.Errorf("列出目录失败: %w", err))
		return
	}

	entries := make([]filesEntry, 0, len(infos))
	for _, info := range infos {
		entries = append(entries, filesEntry{
			Name:  info.Name(),
			Size:  info.Size(),
			IsDir: info.IsDir(),
			Mode:  info.Mode().String(),
			MTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}

	// parent：根目录的 parent 是 null
	parent := ""
	cleaned := filepath.ToSlash(filepath.Clean(req.Path))
	if cleaned != "/" && cleaned != "." {
		parent = filepath.ToSlash(filepath.Dir(cleaned))
	}

	s.audit.Write("files.list", "system", req.System, "server", req.Server, "path", req.Path, "result", "ok", "count", len(entries))
	writeJSON(w, 200, map[string]any{
		"path":    cleaned,
		"parent":  parent,
		"entries": entries,
	})
}

// filesDownloadReq 下载请求体（任意路径）
type filesDownloadReq struct {
	System   string   `json:"system"`
	Server   string   `json:"server"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	Paths    []string `json:"paths"` // 完整远端路径（绝对路径）
	Zip      bool     `json:"zip"`   // 多文件时是否额外打 zip
}

// filesDownloadLimits 下载任务的硬约束（避免误操作 / 连接卡死）
const (
	filesMaxFilesPerTask = 100              // 单次最多 100 个文件
	filesDownloadTimeout = 30 * time.Minute // 单个下载任务总超时（dlmanager.Session idleGC 也是 30min）
)

// handleFilesDownload 启动一个"任意路径下载"任务，立即返回 id。
//
// 路径不做白名单校验（v0.3 自由模式）；只校验：
//   - 每个 path 非空、以 "/" 开头；
//   - 不含 NUL/换行等控制字符；
//   - 总数 ≤ filesMaxFilesPerTask；
//
// 真实访问控制由 SSH 服务器端承担。
//
// 下载机制复用 dlmanager.Session + dlmanager.Manager（ID 全局唯一）。
func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req filesDownloadReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if len(req.Paths) == 0 {
		writeErr(w, 400, errors.New("paths 不能为空"))
		return
	}
	if len(req.Paths) > filesMaxFilesPerTask {
		writeErr(w, 400, fmt.Errorf("单次最多下载 %d 个文件", filesMaxFilesPerTask))
		return
	}
	for _, p := range req.Paths {
		if p == "" {
			writeErr(w, 400, errors.New("path 不能为空"))
			return
		}
		if !strings.HasPrefix(p, "/") {
			writeErr(w, 400, fmt.Errorf("path 必须是绝对路径: %q", p))
			return
		}
		if strings.ContainsAny(p, "\x00\n\r") {
			writeErr(w, 400, fmt.Errorf("path 含非法字符: %q", p))
			return
		}
	}
	_, srv, ok := s.cur().FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
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

	id := dlmanager.NewID()
	sessCtx, cancel := context.WithCancel(context.Background())
	sess := &dlmanager.Session{
		ID:        id,
		Kind:      "files",
		System:    req.System,
		Server:    req.Server,
		Paths:     append([]string(nil), req.Paths...),
		Zip:       req.Zip,
		Folder:    s.cur().DownloadDir(),
		CreatedAt: time.Now(),
	}
	sess.AttachCancel(cancel)
	s.downloads.Create(sess)
	go s.downloads.IdleGC(sess)

	writeJSON(w, 200, map[string]any{"id": id})

	// 异步执行；用独立 ctx，不依赖 r.Context()（请求结束就断开）
	go s.runFilesDownloadTask(sessCtx, sess, srv, creds.Username, creds.Password)
}

// runFilesDownloadTask 后台执行：Dial SSH → 开 SFTP → 串行下每个路径 → （可选）打 zip
func (s *Server) runFilesDownloadTask(
	ctx context.Context,
	sess *dlmanager.Session,
	srv *config.ServerConfig,
	username, password string,
) {
	dialCtx, cancelDial := context.WithTimeout(ctx, 10*time.Second)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: password}, 10*time.Second)
	cancelDial()
	if err != nil {
		s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "dial", "err", err.Error())
		sess.MarkFinished(nil, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
defer cli.Close()

	sftpCli, err := sftpDialer(cli)
	if err != nil {
		sess.MarkFinished(nil, fmt.Errorf("SFTP 打开失败: %w", err))
		return
	}
	defer sftpCli.Close()

	results, err := s.downloadSeriesFree(ctx, srv, sess.Paths, sftpCli, sess)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "cancel")
			sess.MarkFinished(results, fmt.Errorf("已取消"))
			return
		}
		s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "download", "err", err.Error())
		sess.MarkFinished(results, err)
		return
	}

	// 可选 zip（>= 2 个文件才打）
	if sess.Zip && len(results) >= 2 {
		zipName := fmt.Sprintf("%s_files_%s.zip",
			sanitize(srv.Name), time.Now().Format("150405"))
		zipPath := filepath.Join(s.cur().DownloadDir(), results[0].Date, zipName)
		localPaths := make([]string, 0, len(results))
		remoteNames := make([]string, 0, len(results))
		for _, it := range results {
			p := filepath.Join(s.cur().DownloadDir(), it.Date, it.Local)
			localPaths = append(localPaths, p)
			remoteNames = append(remoteNames, it.Remote)
		}
		if err := zipFiles(localPaths, zipPath); err != nil {
			s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "zip", "err", err.Error())
			sess.MarkFinished(results, fmt.Errorf("打包 zip 失败: %w", err))
			return
		}
		st, _ := os.Stat(zipPath)
		var size int64
		if st != nil {
			size = st.Size()
		}
		results = append(results, dlmanager.Item{
			Local: zipName,
			Bytes: strconv.FormatInt(size, 10),
			Date:  results[0].Date,
			Kind:  "zip",
		})
		_ = downloads.WriteMeta(zipPath, downloads.Meta{
			System: sess.System,
			Server: srv.Name,
			Host:   fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			Files:  remoteNames,
			Kind:   "zip",
		})
	}

	s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "ok", "files", len(results), "selected", len(sess.Paths))
	sess.MarkFinished(results, nil)
}

// downloadSeriesFree 串行下多个"完整路径"文件，进度通过 session 广播。
//
// 任何一个文件失败立刻返回（已下完的文件留在本地，不删）。
// 本地落点：downloads/YYYYMMDD/server_basename_HHMMSS
//   - 用远端路径 basename 当主名，保留原始文件名信息；
//   - 加 _HHMMSS 防止同一文件短时间内重复下载互相覆盖；
//   - 不强加 .log 后缀：浏览器下载任意文件都该是原始名+扩展名。
func (s *Server) downloadSeriesFree(
	ctx context.Context,
	srv *config.ServerConfig,
	paths []string,
	sftpCli sftpClientLike,
	sess *dlmanager.Session,
) ([]dlmanager.Item, error) {
	now := time.Now()
	dateDir := now.Format("20060102")
	hhmm := now.Format("150405")
	targetDir := filepath.Join(s.cur().DownloadDir(), dateDir)

	results := make([]dlmanager.Item, 0, len(paths))
	localPaths := make([]string, 0, len(paths))
	emittedPaths := make(map[string]bool, len(paths))

	for idx, remote := range paths {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if emittedPaths[remote] {
			continue
		}
		emittedPaths[remote] = true

		base := filepath.Base(remote)
		sess.BroadcastEvent("file_start", map[string]any{
			"file":  remote,
			"index": idx,
			"total": len(paths),
		})

		localName := fmt.Sprintf("%s_%s_%s",
			sanitize(srv.Name), sanitize(base), hhmm)
		localPath := filepath.Join(targetDir, localName)

		progress := func(w, t int64) {
			sess.BroadcastEvent("progress", map[string]any{
				"file":    remote,
				"written": w,
				"total":   t,
			})
		}

		bytes, err := sftpCli.DownloadFileWithProgress(remote, localPath, progress)
		if err != nil {
			_ = os.Remove(localPath)
			return results, fmt.Errorf("下载 %s 失败: %w", remote, err)
		}

		results = append(results, dlmanager.Item{
			File:   base,
			Local:  localName,
			Bytes:  strconv.FormatInt(bytes, 10),
			Remote: remote,
			Date:   dateDir,
			Kind:   "file",
		})
		localPaths = append(localPaths, localPath)
		_ = downloads.WriteMeta(localPath, downloads.Meta{
			System: sess.System,
			Server: srv.Name,
			Host:   fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			File:   base,
			Files:  []string{remote},
			Kind:   "file",
		})
		s.audit.Write("files.download", "system", sess.System, "server", srv.Name, "path", remote, "result", "ok", "bytes", bytes)

		sess.BroadcastEvent("file_done", map[string]any{
			"file":  remote,
			"bytes": bytes,
		})
	}
	return results, nil
}

// handleFilesDownloadEventsOrCancel 分发 events / cancel 子路径
func (s *Server) handleFilesDownloadEventsOrCancel(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/files/download/")
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

// streamDownloadEvents 把下载进度 / 状态通过 SSE 推给前端（复用 dlmanager.Session 订阅模型）
func (s *Server) streamDownloadEvents(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := s.downloads.Get(id)
	if !ok {
		writeErr(w, 404, errors.New("下载任务不存在"))
		return
	}
	// 已经结束：直接返回最终结果
	if sess.IsFinished() {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeErrSanitized(w, 500, errors.New("response writer 不支持 flush"))
			return
		}
		var ev []byte
		result, finalErr, folder := sess.Snapshot()
		if finalErr != nil {
			ev = dlmanager.FormatEvent("done", map[string]any{"ok": false, "error": finalErr.Error()})
		} else {
			ev = dlmanager.FormatEvent("done", map[string]any{"ok": true, "downloads": result, "folder": folder})
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", ev)
		_, _ = fmt.Fprintf(w, "event: done\ndata: {}\n\n")
		flusher.Flush()
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErrSanitized(w, 500, errors.New("response writer 不支持 flush"))
		return
	}

	ch, unsub := sess.Subscribe()
	defer unsub()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, open := <-ch:
			if !open {
				_, _ = fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		case <-keepalive.C:
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// cancelDownload 显式取消下载（logs 和 files 共用）
func (s *Server) cancelDownload(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	if !s.downloads.Cancel(id) {
		writeErr(w, 404, errors.New("下载任务不存在"))
		return
	}
	s.audit.Write("files.download", "id", id, "result", "ok", "stage", "cancel")
	writeJSON(w, 200, map[string]any{"ok": true, "id": id})
}
