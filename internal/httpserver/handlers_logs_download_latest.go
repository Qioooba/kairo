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
	"time"

	"ops-toolbox/internal/config"
	"ops-toolbox/internal/dlmanager"
	"ops-toolbox/internal/downloads"
	"ops-toolbox/internal/logquery"
	"ops-toolbox/internal/sshclient"
)

type downloadLatestReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Dir      string `json:"dir"`
	Username string `json:"username"`
	Password string `json:"password"`
	Latest   int    `json:"latest"` // 下载几个最新文件，默认 1
	Zip      bool   `json:"zip"`    // 是否额外打包为 zip（latest>1 时才有意义）
}

// handleDownloadLatest 启动一个"按目录下载最近 N 个文件"任务。
//
// 异步化（项 12 修复）：
//   - 默认返回 {"id": "..."}，前端订阅 /api/logs/download/{id}/events 拿进度；
//   - 兼容老调用：URL 加 ?wait=1 或 body 里 wait:true 时同步阻塞等结果，
//     返回老格式 {downloads, folder}（避免一次性大重构前端，老脚本/单元测试也能继续用）；
//   - 这样前端可以选择走新异步路径拿实时进度，老脚本继续 work。
//
// 模型跟 /api/files/download 一致：dlmanager.Session + SSE。
func (s *Server) handleDownloadLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req downloadLatestReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	_, srv, ok := s.cur().FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
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
	latest := req.Latest
	if latest <= 0 {
		latest = 1
	}
	if latest > 5 {
		latest = 5
	}
	// 老接口兼容：URL 加 ?wait=1 → 同步等待结果
	if r.URL.Query().Get("wait") == "1" {
		s.runLogsDownloadSync(w, r, req, srv, ld, creds.Username, creds.Password, latest)
		return
	}

	// 异步：建 session → 立即返回 id → 后台跑
	id := dlmanager.NewID()
	sessCtx, cancel := context.WithCancel(context.Background())
	sess := &dlmanager.Session{
		ID:        id,
		Kind:      "logs",
		System:    req.System,
		Server:    req.Server,
		Dir:       ld.Path,
		Files:     nil, // latest 模式：启动时还没列文件，下载时再确定
		Latest:    latest,
		Zip:       req.Zip,
		Folder:    s.cur().DownloadDir(),
		CreatedAt: time.Now(),
	}
	sess.AttachCancel(cancel)
	s.downloads.Create(sess)
	go s.downloads.IdleGC(sess)

	writeJSON(w, 200, map[string]any{"id": id})
	// 异步执行；用独立 ctx，不依赖 r.Context()（请求结束就断开）
	go s.runLogsDownloadTask(sessCtx, sess, srv, ld, creds.Username, creds.Password, latest)
}

// runLogsDownloadSync 走同步路径（保留老接口）。给"想拿老格式"的前端 / 测试用。
//
// 跟老实现差不多，区别：抽成独立函数方便复用；
// 不再发 SSE，下载完成直接 writeJSON。
func (s *Server) runLogsDownloadSync(
	w http.ResponseWriter,
	r *http.Request,
	req downloadLatestReq,
	srv *config.ServerConfig,
	ld *config.LogDirEntry,
	username, password string,
	latest int,
) {
	results, folder, httpStatus, httpErr := s.runLogsDownloadOnce(
		r.Context(), req.System, srv, ld, username, password, latest, req.Zip, nil,
	)
	if httpErr != nil {
		writeErrSanitized(w, httpStatus, httpErr)
		return
	}
	writeJSON(w, 200, map[string]any{
		"downloads": results,
		"folder":    folder,
	})
}

// runLogsDownloadTask 后台执行：列文件 → 逐个下载 → （可选）打 zip → mark finished
func (s *Server) runLogsDownloadTask(
	ctx context.Context,
	sess *dlmanager.Session,
	srv *config.ServerConfig,
	ld *config.LogDirEntry,
	username, password string,
	latest int,
) {
	results, _, _, err := s.runLogsDownloadOnce(
		ctx, sess.System, srv, ld, username, password, latest, sess.Zip, sess,
	)
	if err != nil {
		// 取消 → 写 audit + 静默结束
		if errors.Is(err, context.Canceled) {
			s.audit.Write("logs.download", "system", sess.System, "server", sess.Server, "dir", ld.Path, "result", "fail", "stage", "cancel")
			sess.MarkFinished(results, fmt.Errorf("已取消"))
			return
		}
		sess.MarkFinished(results, err)
		return
	}
	sess.MarkFinished(results, nil)
}

// runLogsDownloadOnce 是核心执行逻辑（被 sync 和 async 路径共用）。
//
// 返回值：
//   - results：所有成功下载的 Item（含 zip 产物）
//   - folder：本地落点根目录
//   - httpStatus / httpErr：sync 路径需要的 HTTP 状态码 + 错误；
//     async 路径不消费这两值（错误走 sess.MarkFinished）。
//
// 第七个参数 sess 非 nil 时走异步模式（会 broadcast 进度事件 + 走 IdleGC）；
// nil 时走同步模式（直接返回结果，不发 SSE）。
func (s *Server) runLogsDownloadOnce(
	ctx context.Context,
	system string,
	srv *config.ServerConfig,
	ld *config.LogDirEntry,
	username, password string,
	latest int,
	zip bool,
	sess *dlmanager.Session,
) ([]dlmanager.Item, string, int, error) {
	// SSH Dial 独立 ctx + 统一超时
	dialCtx, cancelDial := context.WithTimeout(ctx, sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
	}, sshclient.Credentials{Password: password}, sshAttemptTimeout)
	cancelDial()
	if err != nil {
		s.audit.Write("logs.download", "system", system, "server", srv.Name, "dir", ld.Path, "result", "fail", "stage", "dial", "err", err.Error())
		return nil, "", 502, fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer cli.Close()

	sftpCli, err := sftpDialer(cli)
	if err != nil {
		return nil, "", 502, fmt.Errorf("SFTP 打开失败: %w", err)
	}
	defer sftpCli.Close()

	// 列文件（独立 ctx，给后续 SFTP 下载 + zip 留够时间）
	runCtx, cancelRun := context.WithTimeout(ctx, 60*time.Second)
	defer cancelRun()
	cmd, err := logquery.ListCommand(ld.Path, ld.Patterns, latest, ld.ListModeFor())
	if err != nil {
		return nil, "", 500, err
	}
	stdout, stderr, code, err := cli.Run(runCtx, cmd, s.cur().SearchTimeout(), ld.Encoding)
	if err != nil || code != 0 {
		s.audit.Write("logs.download", "system", system, "server", srv.Name, "dir", ld.Path, "result", "fail", "stage", "list", "err", trim(stderr, 200))
		return nil, "", 502, fmt.Errorf("列文件失败: %v / %s", err, trim(stderr, 200))
	}
	files, err := logquery.ParseListOutput(stdout)
	if err != nil {
		return nil, "", 500, err
	}
	if len(files) == 0 {
		return nil, "", 404, errors.New("没有匹配的文件")
	}
	if latest > len(files) {
		latest = len(files)
	}

	now := time.Now()
	dateDir := now.Format("20060102")
	// 时间戳用下划线分隔（项 18）：HHMMSS_mmm，比点号在文件名里更稳。
	stamp := now.Format("150405_000")
	// 目录别名：把 log_dir.path 的最后一段当作"dirName"，让多服务器同名日志也能区分
	dirAlias := filepath.Base(ld.Path)
	// 下载文件落点：downloads/YYYYMMDD/serverName_dirName_originalName_HHMMSS_mmm.log
	// （多台服务器同时下载同名 SystemOut.log 时不会互相覆盖）
	targetDir := filepath.Join(s.cur().DownloadDir(), dateDir)
	// 收集已经下到本地的文件路径，zip 时按这个顺序打包
	localPaths := make([]string, 0, latest)
	results := make([]dlmanager.Item, 0, latest+1)

	// 异步：先广播一次 list_done 让前端知道"列文件完成，开始下"
	if sess != nil {
		sess.BroadcastEvent("list_done", map[string]any{
			"count": latest,
			"dir":   ld.Path,
		})
	}

	for i := 0; i < latest; i++ {
		// ctx 取消检查（用户点取消 / IdleGC 超时）
		if cerr := ctx.Err(); cerr != nil {
			return results, s.cur().DownloadDir(), 0, cerr
		}
		f := files[i]
		remote := filepath.ToSlash(filepath.Join(ld.Path, f.Name))
		localName := fmt.Sprintf("%s_%s_%s_%s.log",
			sanitize(srv.Name), sanitize(dirAlias), sanitize(f.Name), stamp)
		localPath := filepath.Join(targetDir, localName)

		// 异步：广播 file_start
		if sess != nil {
			sess.BroadcastEvent("file_start", map[string]any{
				"file":  remote,
				"index": i,
				"total": latest,
			})
		}

		// 进度回调（仅异步；同步模式不接 progress）
		progress := func(w, t int64) {
			if sess != nil {
				sess.BroadcastEvent("progress", map[string]any{
					"file":    remote,
					"written": w,
					"total":   t,
				})
			}
		}
		bytes, err := sftpCli.DownloadFileWithProgress(remote, localPath, progress)
		if err != nil {
			s.audit.Write("logs.download", "system", system, "server", srv.Name, "dir", ld.Path, "file", f.Name, "result", "fail", "err", err.Error())
			// 异步：广播错误行让前端知道
			if sess != nil {
				sess.BroadcastEvent("file_fail", map[string]any{
					"file":  remote,
					"error": err.Error(),
				})
			}
			return results, s.cur().DownloadDir(), 502, fmt.Errorf("下载 %s 失败: %w", f.Name, err)
		}
		results = append(results, dlmanager.Item{
			File:    f.Name,
			Local:   localName,
			Bytes:   strconv.FormatInt(bytes, 10),
			Remote:  remote,
			Date:    dateDir,
			Kind:    "file",
			AbsPath: localPath, // v0.5-F：让前端能"在文件管理器中显示"
		})
		localPaths = append(localPaths, localPath)
		// 写 sidecar 元数据 — 给"下载历史"页用
		_ = downloads.WriteMeta(localPath, downloads.Meta{
			System:   system,
			Server:   srv.Name,
			Host:     fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			Dir:      ld.Path,
			DirAlias: dirAlias,
			File:     f.Name,
			Encoding: ld.Encoding,
			Kind:     "file",
		})
		s.audit.Write("logs.download", "system", system, "server", srv.Name, "dir", ld.Path, "file", f.Name, "result", "ok", "bytes", bytes)

		// 异步：广播 file_done
		if sess != nil {
			sess.BroadcastEvent("file_done", map[string]any{
				"file":  remote,
				"bytes": bytes,
			})
		}
	}
	// 是否额外打 zip？
	if zip && latest >= 2 {
		zipName := fmt.Sprintf("%s_%s_latest_%s.zip",
			sanitize(srv.Name), sanitize(dirAlias), stamp)
		zipPath := filepath.Join(targetDir, zipName)
		// 项 17：用远端原始文件名打包（传 ZipSource{NameInZip}）
		sources := make([]ZipSource, 0, len(localPaths))
		for i, lp := range localPaths {
			sources = append(sources, ZipSource{
				Path:      lp,
				NameInZip: files[i].Name, // 远端原始文件名
			})
		}
		if err := zipFilesNamed(sources, zipPath); err != nil {
			s.audit.Write("logs.download", "system", system, "server", srv.Name, "dir", ld.Path, "result", "fail", "stage", "zip", "err", err.Error())
			if sess != nil {
				sess.BroadcastEvent("file_fail", map[string]any{
					"file":  zipName,
					"error": err.Error(),
				})
			}
			return results, s.cur().DownloadDir(), 502, fmt.Errorf("打包 zip 失败: %w", err)
		}
		st, statErr := os.Stat(zipPath)
		var size int64
		if statErr == nil {
			size = st.Size()
		}
		results = append(results, dlmanager.Item{
			File:    "",
			Local:   zipName,
			Bytes:   strconv.FormatInt(size, 10),
			Remote:  "",
			Date:    dateDir,
			Kind:    "zip",
			AbsPath: zipPath, // v0.5-F：让前端能"在文件管理器中显示"
		})
		// 写 zip 的 sidecar：把包含的远端文件名记下来
		fileNames := make([]string, 0, len(localPaths))
		for i := 0; i < latest && i < len(files); i++ {
			fileNames = append(fileNames, files[i].Name)
		}
		_ = downloads.WriteMeta(zipPath, downloads.Meta{
			System:   system,
			Server:   srv.Name,
			Host:     fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			Dir:      ld.Path,
			DirAlias: dirAlias,
			Files:    fileNames,
			Encoding: ld.Encoding,
			Kind:     "zip",
		})
		s.audit.Write("logs.download", "system", system, "server", srv.Name, "dir", ld.Path, "result", "ok", "stage", "zip", "files", latest, "bytes", size)

		if sess != nil {
			sess.BroadcastEvent("file_done", map[string]any{
				"file":  zipName,
				"bytes": size,
			})
		}
	}
	return results, s.cur().DownloadDir(), 0, nil
}
