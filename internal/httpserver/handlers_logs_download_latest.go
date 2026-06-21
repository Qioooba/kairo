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

	"ops-toolbox/internal/dlmanager"
	"ops-toolbox/internal/downloads"
	"ops-toolbox/internal/logquery"
	"ops-toolbox/internal/sftpclient"
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
	username := creds.Username
	latest := req.Latest
	if latest <= 0 {
		latest = 1
	}
	if latest > 5 {
		latest = 5
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: creds.Password}, 10*time.Second)
	if err != nil {
		auditErr(w, s.audit, "logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", err)
		writeErr(w, 502, err)
		return
	}
	defer cli.Close()

	sftpCli, err := sftpclient.New(cli.RawConn())
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	defer sftpCli.Close()

	// 列文件
	cmd, err := logquery.ListCommand(ld.Path, ld.Patterns, latest)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	stdout, stderr, code, err := cli.Run(ctx, cmd, s.cur().SearchTimeout(), ld.Encoding)
	if err != nil || code != 0 {
		s.audit.Write("logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", "err", trim(stderr, 200))
		writeErr(w, 502, fmt.Errorf("列文件失败: %v / %s", err, trim(stderr, 200)))
		return
	}
	files, err := logquery.ParseListOutput(stdout)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if len(files) == 0 {
		writeErr(w, 404, errors.New("没有匹配的文件"))
		return
	}
	if latest > len(files) {
		latest = len(files)
	}

	results := make([]dlmanager.Item, 0, latest+1)
	now := time.Now()
	dateDir := now.Format("20060102")
	hhmm := now.Format("150405")
	// 目录别名：把 log_dir.path 的最后一段当作"dirName"，让多服务器同名日志也能区分
	dirAlias := filepath.Base(ld.Path)
	// 下载文件落点：downloads/YYYYMMDD/serverName_dirName_originalName_HHMMSS.log
	// （多台服务器同时下载同名 SystemOut.log 时不会互相覆盖）
	targetDir := filepath.Join(s.cur().DownloadDir(), dateDir)
	// 收集已经下到本地的文件路径，zip 时按这个顺序打包
	localPaths := make([]string, 0, latest)
	for i := 0; i < latest; i++ {
		f := files[i]
		remote := filepath.ToSlash(filepath.Join(ld.Path, f.Name))
		localName := fmt.Sprintf("%s_%s_%s_%s.log",
			sanitize(srv.Name), sanitize(dirAlias), sanitize(f.Name), hhmm)
		localPath := filepath.Join(targetDir, localName)
		bytes, err := sftpCli.DownloadFile(remote, localPath)
		if err != nil {
			s.audit.Write("logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "file", f.Name, "result", "fail", "err", err.Error())
			writeErr(w, 502, fmt.Errorf("下载 %s 失败: %w", f.Name, err))
			return
		}
		results = append(results, dlmanager.Item{
			File:   f.Name,
			Local:  localName,
			Bytes:  strconv.FormatInt(bytes, 10),
			Remote: remote,
			Date:   dateDir,
			Kind:   "file",
		})
		localPaths = append(localPaths, localPath)
		// 写 sidecar 元数据 — 给"下载历史"页用
		_ = downloads.WriteMeta(localPath, downloads.Meta{
			System:   req.System,
			Server:   srv.Name,
			Host:     fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			Dir:      ld.Path,
			DirAlias: dirAlias,
			File:     f.Name,
			Encoding: ld.Encoding,
			Kind:     "file",
		})
		s.audit.Write("logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "file", f.Name, "result", "ok", "bytes", bytes)
	}
	// 是否额外打 zip？
	// 规则：
	//   - latest=1 时 zip 没意义，忽略；
	//   - 显式 req.Zip=true 才打，避免默认行为变化让老用户惊喜；
	//   - zip 落点和原始文件同一个 dateDir，文件名加 _pack 后缀以示区分。
	if req.Zip && latest >= 2 {
		zipName := fmt.Sprintf("%s_%s_latest_%s.zip",
			sanitize(srv.Name), sanitize(dirAlias), hhmm)
		zipPath := filepath.Join(targetDir, zipName)
		if err := zipFiles(localPaths, zipPath); err != nil {
			s.audit.Write("logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", "stage", "zip", "err", err.Error())
			writeErr(w, 502, fmt.Errorf("打包 zip 失败: %w", err))
			return
		}
		st, statErr := os.Stat(zipPath)
		var size int64
		if statErr == nil {
			size = st.Size()
		}
		results = append(results, dlmanager.Item{
			File:   "",
			Local:  zipName,
			Bytes:  strconv.FormatInt(size, 10),
			Remote: "",
			Date:   dateDir,
			Kind:   "zip",
		})
		// 写 zip 的 sidecar：把包含的远端文件名记下来
		fileNames := make([]string, 0, len(localPaths))
		for i := 0; i < latest && i < len(files); i++ {
			fileNames = append(fileNames, files[i].Name)
		}
		_ = downloads.WriteMeta(zipPath, downloads.Meta{
			System:   req.System,
			Server:   srv.Name,
			Host:     fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			Dir:      ld.Path,
			DirAlias: dirAlias,
			Files:    fileNames,
			Encoding: ld.Encoding,
			Kind:     "zip",
		})
		s.audit.Write("logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "ok", "stage", "zip", "files", latest, "bytes", size)
	}
	writeJSON(w, 200, map[string]any{
		"downloads": results,
		"folder":    s.cur().DownloadDir(),
	})
}
