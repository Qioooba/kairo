package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"kairo/internal/waspack"
)

type waspackReq struct {
	ProjectDir  string `json:"project_dir"`
	OutputDir   string `json:"output_dir"`
	PackageName string `json:"package_name"`
	Manifest    string `json:"manifest"`
	AutoPair    *bool  `json:"auto_pair"`
}

func (s *Server) handleWASPackPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	req, err := decodeWASPackReq(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	pv, err := waspack.PreviewRequest(toWASPackReq(req))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.audit.Write("waspack.preview", "project", req.ProjectDir, "files", len(pv.Files), "missing", len(pv.Missing), "result", "ok")
	writeJSON(w, 200, map[string]any{
		"ok":       true,
		"files":    pv.Files,
		"missing":  pv.Missing,
		"warnings": pv.Warnings,
		"stats":    pv.Stats,
	})
}

func (s *Server) handleWASPackBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	req, err := decodeWASPackReq(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(req.OutputDir) == "" {
		writeErr(w, 400, errors.New("请填写要生成的文件夹路径"))
		return
	}
	res, err := waspack.Build(toWASPackReq(req))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.audit.Write("waspack.build", "project", req.ProjectDir, "output", res.OutputDir, "tar", res.TarFile, "files", res.Files, "result", "ok")
	writeJSON(w, 200, res)
}

type waspackOpenReq struct {
	OutputDir string `json:"output_dir"`
}

func (s *Server) handleWASPackOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req waspackOpenReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	dir := strings.TrimSpace(req.OutputDir)
	if dir == "" {
		writeErr(w, 400, errors.New("请先填写打包文件夹路径"))
		return
	}
	if strings.ContainsAny(dir, "\x00\n\r") {
		writeErr(w, 400, errors.New("路径含非法字符"))
		return
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("路径无效: %w", err))
		return
	}
	st, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, 400, errors.New("打包文件夹还不存在，请先生成投产包"))
			return
		}
		writeErr(w, 400, err)
		return
	}
	if st.Mode()&os.ModeSymlink != 0 {
		writeErr(w, 400, errors.New("拒绝打开符号链接"))
		return
	}
	if !st.IsDir() {
		writeErr(w, 400, errors.New("路径不是文件夹"))
		return
	}
	if err := openFolderInFileManager(abs); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("waspack.open", "path", abs, "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true, "path": abs})
}

func decodeWASPackReq(r *http.Request) (waspackReq, error) {
	var req waspackReq
	if err := json.NewDecoder(io.LimitReader(r.Body, waspack.MaxManifestBytes+16*1024)).Decode(&req); err != nil {
		return req, fmt.Errorf("请求体解析失败: %w", err)
	}
	req.ProjectDir = strings.TrimSpace(req.ProjectDir)
	req.OutputDir = strings.TrimSpace(req.OutputDir)
	req.PackageName = strings.TrimSpace(req.PackageName)
	if req.ProjectDir == "" {
		return req, errors.New("请选择本地 credit 工程目录")
	}
	if strings.TrimSpace(req.Manifest) == "" {
		return req, errors.New("请粘贴投产清单")
	}
	return req, nil
}

func toWASPackReq(req waspackReq) waspack.Request {
	auto := true
	if req.AutoPair != nil {
		auto = *req.AutoPair
	}
	return waspack.Request{
		ProjectDir:  req.ProjectDir,
		OutputDir:   req.OutputDir,
		PackageName: req.PackageName,
		Manifest:    req.Manifest,
		AutoPair:    auto,
	}
}
