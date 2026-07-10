package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kairo/internal/config"
	"kairo/internal/sftpclient"
	"kairo/internal/sysutil"
	"kairo/internal/sshclient"
)

const (
	editTempDir            = "edit-temp"
	editMaxFileSize        = 200 * 1024 * 1024
	editWatcherWait        = 500 * time.Millisecond
	editMaxIdleTime        = 24 * time.Hour
	editWatcherIdleTimeout = 2 * time.Hour
)

type editTask struct {
	id         string
	system     string
	server     string
	username   string
	password   string
	remotePath string
	localPath  string
	opener     *config.ExternalOpener
	done       chan struct{}
	editorDone chan struct{}
	events     chan editEvent
	finished   chan struct{}
	mu         sync.Mutex
	closed     bool
	closeOnce  sync.Once
}

func (t *editTask) signalStop() {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		close(t.done)
	})
}

type editEvent struct {
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
}

var editTasks sync.Map

type sshSftpEditReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
	Path     string `json:"path"`
	Opener   string `json:"opener"`
}

type sshSftpEditResp struct {
	ID        string `json:"id"`
	LocalPath string `json:"local_path"`
	Opener    string `json:"opener"`
}

func (s *Server) handleSshSftpEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshSftpEditReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}

	if strings.TrimSpace(req.System) == "" || strings.TrimSpace(req.Server) == "" {
		writeErr(w, 400, errors.New("system / server 不能为空"))
		return
	}
	if req.Path == "" {
		writeErr(w, 400, errors.New("path 不能为空"))
		return
	}
	if !strings.HasPrefix(req.Path, "/") {
		writeErr(w, 400, fmt.Errorf("path 必须是绝对路径: %q", req.Path))
		return
	}
	if strings.ContainsAny(req.Path, "\x00\n\r") {
		writeErr(w, 400, fmt.Errorf("path 含非法字符: %q", req.Path))
		return
	}
	if strings.TrimSpace(req.Opener) == "" {
		writeErr(w, 400, errors.New("opener 不能为空"))
		return
	}

	cur := s.cur()
	op := cur.App.FindOpener(req.Opener)
	if op == nil {
		writeErr(w, 404, fmt.Errorf("未找到打开器 %q（请先在系统配置里添加）", req.Opener))
		return
	}
	if strings.TrimSpace(op.Path) == "" {
		writeErr(w, 500, errors.New("打开器 path 为空（配置异常）"))
		return
	}

	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), sshDialOuterTimeout)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: creds.Username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: cur.App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
	if err != nil {
		s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "stage", "dial", "err", err.Error())
		writeErrSanitized(w, 502, err)
		return
	}
	defer cli.Close()

	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "stage", "sftp", "err", err.Error())
		writeErrSanitized(w, 502, fmt.Errorf("SFTP 打开失败: %w", err))
		return
	}
	defer sftpCli.Close()

	info, err := sftpCli.Stat(req.Path)
	if err != nil {
		s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "stage", "stat", "err", err.Error())
		writeErrSanitized(w, 502, fmt.Errorf("stat 失败: %w", err))
		return
	}
	if info.IsDir() {
		writeErr(w, 400, fmt.Errorf("不支持编辑目录: %s", req.Path))
		return
	}
	if info.Size() > editMaxFileSize {
		writeErr(w, 400, fmt.Errorf("文件超过大小限制 %d MB", editMaxFileSize/1024/1024))
		return
	}

	tempDir := filepath.Join(cur.DataDir(), editTempDir)
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "stage", "mkdir", "err", err.Error())
		writeErr(w, 500, fmt.Errorf("创建临时目录失败: %w", err))
		return
	}

	hash := sha256.Sum256([]byte(req.System + req.Server + req.Path))
	taskID := fmt.Sprintf("%x", hash[:16])

	// taskID 是 system+server+path 的确定性哈希。对同一文件再次点击 "编辑"
	// 如果不显式清理旧 watcher，老 watcher 退出时 editTasks.Delete(task.id)
	// 会误删掉新 task、导致 SSE 立刻 404、且老 watcher 与新 watcher 都会在
	// mtime 变化时上传 → 重复上传 + 重复 SSH 拨号。这里主动关闭旧 task 等
	// 它完全退出（finished 通道关闭），再接管同一 taskID。
	if existing, ok := editTasks.Load(taskID); ok {
		if old, ok2 := existing.(*editTask); ok2 && old != nil {
			old.signalStop()
			select {
			case <-old.finished:
			case <-time.After(3 * time.Second):
			}
			editTasks.CompareAndDelete(taskID, old)
		}
	}

	taskDir := filepath.Join(tempDir, taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "stage", "taskdir", "err", err.Error())
		writeErr(w, 500, fmt.Errorf("创建任务目录失败: %w", err))
		return
	}

	// 拒绝 base 为 "." / ".." / 空：filepath.Base("/foo/..") 返回 ".."，会让
	// localPath 变成 taskDir 本身而不是文件。
	base := filepath.Base(req.Path)
	if base == "" || base == "." || base == ".." {
		s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "stage", "basename", "err", "invalid base")
		os.RemoveAll(taskDir)
		writeErr(w, 400, fmt.Errorf("无法编辑路径 %q（base 无效）", req.Path))
		return
	}
	localPath := filepath.Join(taskDir, base)
	if _, err := sftpCli.DownloadFile(req.Path, localPath); err != nil {
		s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "stage", "download", "err", err.Error())
		writeErrSanitized(w, 502, fmt.Errorf("下载文件失败: %w", err))
		// 与 cmd.Start 失败路径保持一致：失败时清掉 taskDir，不留半截状态。
		os.RemoveAll(taskDir)
		return
	}

	task := &editTask{
		id:         taskID,
		system:     req.System,
		server:     req.Server,
		username:   creds.Username,
		password:   creds.Password,
		remotePath: req.Path,
		localPath:  localPath,
		opener:     op,
		done:       make(chan struct{}),
		editorDone: make(chan struct{}),
		finished:   make(chan struct{}),
		events:     make(chan editEvent, 16),
	}
	editTasks.Store(taskID, task)

	cmd := exec.Command(op.Path, localPath)
	sysutil.HideConsoleWindow(cmd)
	if err := cmd.Start(); err != nil {
		s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "stage", "open", "err", err.Error())
		os.RemoveAll(taskDir)
		editTasks.Delete(taskID)
		writeErrSanitized(w, 500, fmt.Errorf("启动打开器 %q 失败: %w", op.Name, err))
		return
	}
	go func() {
		_ = cmd.Wait()
		close(task.editorDone)
	}()

	go s.watchAndUploadEdit(task, srv)

	s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
		"path", req.Path, "result", "ok", "opener", op.Name)
	writeJSON(w, 200, sshSftpEditResp{
		ID:        taskID,
		LocalPath: localPath,
		Opener:    op.Name,
	})
}

func (s *Server) watchAndUploadEdit(task *editTask, srv *config.ServerConfig) {
	defer close(task.finished)

	cleanup := func() {
		task.signalStop()
		editTasks.CompareAndDelete(task.id, task)
	}
	defer cleanup()

	// 初始化 lastMod 为文件当前 ModTime，避免首次轮询把"刚下载未修改"的文件上传回去
	fi0, err := os.Stat(task.localPath)
	if err != nil {
		return
	}
	lastMod := fi0.ModTime()
	lastActivity := time.Now()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// 编辑器进程退出只通知前端，不再结束任务。
	// 原因：单实例编辑器（Notepad++ / VS Code / Sublime 等）的启动器进程会立刻退出，
	// 但用户实际还在另一个常驻实例里编辑文件。若在此处 closeTask()，监控循环会在
	// ~2s 内退出，用户后续保存永远检测不到。任务靠 editWatcherIdleTimeout 自然回收。
	// 注意：cmd.Start() 失败时不会启动下面的 wait goroutine，editorDone 永远不关闭。
	// 这里加 task.done 分支：task 被释放（taskID 冲突、idle 超时等）后直接退出，避免泄漏。
	go func() {
		select {
		case <-task.editorDone:
			time.Sleep(2 * time.Second)
			sendEditEvent(task, editEvent{Kind: "editor_closed"})
		case <-task.done:
		}
	}()

	for {
		select {
		case <-task.done:
			return
		case <-ticker.C:
			task.mu.Lock()
			if task.closed {
				task.mu.Unlock()
				return
			}
			task.mu.Unlock()

			// idle 超时：长时间没有任何文件改动，认为用户已停止编辑，回收任务。
			if time.Since(lastActivity) > editWatcherIdleTimeout {
				return
			}

			fi, err := os.Stat(task.localPath)
			if err != nil {
				if os.IsNotExist(err) {
					// Notepad++ / VS Code 等编辑器的"安全保存"会先写临时文件 → 删除原文件 →
					// 重命名临时文件为原文件名，期间 os.Stat 会短暂返回 IsNotExist。
					// 旧实现这里直接 return 会让监控 goroutine 提前退出，后续真正保存永远检测不到。
					// 改为 continue：文件重新出现后下一轮 ticker 自然会取到新 ModTime 并上传；
					// 真正的文件删除由 editWatcherIdleTimeout（2 小时无活动）兜底回收任务。
					continue
				}
				continue
			}

			if fi.ModTime().Equal(lastMod) {
				continue
			}

			time.Sleep(editWatcherWait)

			fi2, err := os.Stat(task.localPath)
			if err != nil {
				continue
			}
			if !fi2.ModTime().Equal(fi.ModTime()) {
				lastMod = fi2.ModTime()
				continue
			}

			lastMod = fi.ModTime()
			lastActivity = time.Now()
			sendEditEvent(task, editEvent{Kind: "upload_start"})

			if err := s.uploadEditFile(task, srv); err != nil {
				s.audit.Write("ssh.sftp.edit.upload", "system", task.system,
					"server", task.server, "path", task.remotePath,
					"result", "fail", "err", err.Error())
				sendEditEvent(task, editEvent{Kind: "upload_fail", Message: err.Error()})
			} else {
				s.audit.Write("ssh.sftp.edit.upload", "system", task.system,
					"server", task.server, "path", task.remotePath,
					"result", "ok")
				sendEditEvent(task, editEvent{Kind: "upload_ok", Bytes: fi2.Size()})
			}
		}
	}
}

func sendEditEvent(task *editTask, ev editEvent) {
	defer func() { _ = recover() }()
	select {
	case task.events <- ev:
	default:
	}
}

func (s *Server) uploadEditFile(task *editTask, srv *config.ServerConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), sshDialOuterTimeout)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: task.username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: task.password}, sshAttemptTimeout)
	if err != nil {
		return fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer cli.Close()

	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		return fmt.Errorf("SFTP 打开失败: %w", err)
	}
	defer sftpCli.Close()

	// 上传前重新校验文件大小（用户可能在编辑器里粘贴大段内容）
	fi, err := os.Stat(task.localPath)
	if err != nil {
		return fmt.Errorf("stat 本地文件失败: %w", err)
	}
	if fi.Size() > editMaxFileSize {
		return fmt.Errorf("文件超过 %d MB 限制，已跳过上传", editMaxFileSize/1024/1024)
	}

	// 原子落盘：先写 .partial 再 Rename，避免网络中断导致远端文件半截损坏。
	// partial 与目标在同一目录，保证同文件系统 Rename 是原子的。
	partialPath := task.remotePath + ".kairo-edit.partial"
	if err := sftpCli.UploadFile(task.localPath, partialPath, 0); err != nil {
		// 清理残留的 partial（忽略清理失败）
		_ = sftpCli.Remove(partialPath)
		return fmt.Errorf("上传到临时文件失败: %w", err)
	}
	if err := sftpCli.Rename(partialPath, task.remotePath); err != nil {
		// Rename 失败：远端 partial 残留，但原文件未被破坏
		_ = sftpCli.Remove(partialPath)
		return fmt.Errorf("原子重命名失败（原文件未受影响）: %w", err)
	}
	return nil
}

func (s *Server) handleSshSftpEditEvents(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/ssh/sftp/edit/"), "/")
	if len(parts) != 2 || parts[1] != "events" {
		http.NotFound(w, r)
		return
	}
	taskID := parts[0]
	taskAny, ok := editTasks.Load(taskID)
	if !ok {
		writeErr(w, 404, errors.New("编辑任务不存在或已结束"))
		return
	}
	task := taskAny.(*editTask)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, errors.New("SSE 不支持"))
		return
	}

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case ev := <-task.events:
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, string(data))
			flusher.Flush()
		case <-pingTicker.C:
			fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		case <-task.done:
			fmt.Fprintf(w, "event: done\ndata: {}\n\n")
			flusher.Flush()
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) cleanupEditTempFiles() {
	tempDir := filepath.Join(s.cur().DataDir(), editTempDir)
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(tempDir, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > editMaxIdleTime {
			_ = os.RemoveAll(path)
		}
	}
}
