package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ops-toolbox/internal/sshclient"
)

type sshTestReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleSSHTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshTestReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
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
	username := creds.Username

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: creds.Password}, 10*time.Second)
	if err != nil {
		clean := sshclient.SanitizeError(err.Error())
		s.audit.Write("ssh.test", "system", req.System, "server", req.Server, "result", "fail", "err", clean)
		writeErr(w, 502, errors.New(clean))
		return
	}
	defer cli.Close()
	// 跑一条无害命令确认可执行
	stdout, _, code, err := cli.Run(ctx, "echo ok", 5*time.Second, "")
	if err != nil || code != 0 || strings.TrimSpace(stdout) != "ok" {
		s.audit.Write("ssh.test", "system", req.System, "server", req.Server, "result", "fail", "err", "echo failed")
		writeErr(w, 502, fmt.Errorf("连接成功但命令执行失败: code=%d err=%v", code, err))
		return
	}
	s.audit.Write("ssh.test", "system", req.System, "server", req.Server, "result", "ok")
	writeJSON(w, 200, map[string]any{
		"ok":     true,
		"server": req.Server,
		"system": req.System,
	})
}
