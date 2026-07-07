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
	editTempDir     = "edit-temp"
	editMaxFileSize = 200 * 1024 * 1024
	editWatcherWait = 500 * time.Millisecond
	editMaxIdleTime = 24 * time.Hour
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
	mu         sync.Mutex
	closed     bool
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
	taskDir := filepath.Join(tempDir, taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "stage", "taskdir", "err", err.Error())
		writeErr(w, 500, fmt.Errorf("创建任务目录失败: %w", err))
		return
	}

	localPath := filepath.Join(taskDir, filepath.Base(req.Path))
	if _, err := sftpCli.DownloadFile(req.Path, localPath); err != nil {
		s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "stage", "download", "err", err.Error())
		writeErrSanitized(w, 502, fmt.Errorf("下载文件失败: %w", err))
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
	}
	editTasks.Store(taskID, task)

	go s.watchAndUploadEdit(task, srv)

	cmd := exec.Command(op.Path, localPath)
	sysutil.HideConsoleWindow(cmd)
	if err := cmd.Start(); err != nil {
		s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "stage", "open", "err", err.Error())
		writeErrSanitized(w, 500, fmt.Errorf("启动打开器 %q 失败: %w", op.Name, err))
		return
	}
	go func() { _ = cmd.Wait() }()

	s.audit.Write("ssh.sftp.edit", "system", req.System, "server", req.Server,
		"path", req.Path, "result", "ok", "opener", op.Name)
	writeJSON(w, 200, sshSftpEditResp{
		ID:        taskID,
		LocalPath: localPath,
		Opener:    op.Name,
	})
}

func (s *Server) watchAndUploadEdit(task *editTask, srv *config.ServerConfig) {
	defer func() {
		task.mu.Lock()
		task.closed = true
		task.mu.Unlock()
		close(task.done)
		editTasks.Delete(task.id)
	}()

	var lastMod time.Time
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

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

			fi, err := os.Stat(task.localPath)
			if err != nil {
				if os.IsNotExist(err) {
					return
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

			if err := s.uploadEditFile(task, srv); err != nil {
				s.audit.Write("ssh.sftp.edit.upload", "system", task.system,
					"server", task.server, "path", task.remotePath,
					"result", "fail", "err", err.Error())
			} else {
				s.audit.Write("ssh.sftp.edit.upload", "system", task.system,
					"server", task.server, "path", task.remotePath,
					"result", "ok")
			}
		}
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

	return sftpCli.UploadFile(task.localPath, task.remotePath, 0)
}

func (s *Server) handleSshSftpEditEvents(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/ssh/sftp/edit/"), "/")
	if len(parts) != 2 || parts[1] != "events" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
			w.(http.Flusher).Flush()
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
