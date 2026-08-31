package httpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kairo/internal/comparefs"
	"kairo/internal/sftpclient"
	"kairo/internal/sshclient"
	"kairo/internal/textcodec"
)

const (
	compareReadLimit = 8 * 1024 * 1024
	compareMaxFiles  = 50000
)

type compareSourceSpec struct {
	Kind               string `json:"kind"`
	Path               string `json:"path"`
	System             string `json:"system,omitempty"`
	Server             string `json:"server,omitempty"`
	Username           string `json:"username,omitempty"`
	Password           string `json:"password,omitempty"`
	Host               string `json:"host,omitempty"`
	Port               int    `json:"port,omitempty"`
	TLSMode            string `json:"tls_mode,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
	Encoding           string `json:"encoding,omitempty"`
}

type compareListReq struct {
	Source compareSourceSpec `json:"source"`
}
type compareReadReq struct {
	Source   compareSourceSpec `json:"source"`
	MaxBytes int64             `json:"max_bytes,omitempty"`
}
type compareReadResp struct {
	Text      string            `json:"text"`
	Entry     comparefs.Entry   `json:"entry"`
	Version   comparefs.Version `json:"version"`
	Truncated bool              `json:"truncated"`
	Binary    bool              `json:"binary"`
	Encoding  string            `json:"encoding"`
	EOL       string            `json:"eol"`
	BOM       bool              `json:"bom"`
}
type compareWriteReq struct {
	Target   compareSourceSpec  `json:"target"`
	Content  string             `json:"content"`
	Expected *comparefs.Version `json:"expected,omitempty"`
	Backup   bool               `json:"backup"`
	Encoding string             `json:"encoding,omitempty"`
	EOL      string             `json:"eol,omitempty"`
	BOM      bool               `json:"bom,omitempty"`
}
type compareCopyReq struct {
	Source   compareSourceSpec  `json:"source"`
	Target   compareSourceSpec  `json:"target"`
	Expected *comparefs.Version `json:"expected,omitempty"`
	Backup   bool               `json:"backup"`
}
type compareSyncItem struct {
	RelPath  string             `json:"rel_path"`
	Expected *comparefs.Version `json:"expected,omitempty"`
}
type compareSyncReq struct {
	Left      compareSourceSpec `json:"left"`
	Right     compareSourceSpec `json:"right"`
	Direction string            `json:"direction"`
	Items     []compareSyncItem `json:"items"`
	Backup    bool              `json:"backup"`
}
type compareSyncFailure struct {
	RelPath string `json:"rel_path"`
	Error   string `json:"error"`
}
type compareSyncResult struct {
	Copied   int                  `json:"copied"`
	Failed   int                  `json:"failed"`
	Bytes    int64                `json:"bytes"`
	Failures []compareSyncFailure `json:"failures,omitempty"`
}
type compareScanReq struct {
	Left                 compareSourceSpec `json:"left"`
	Right                compareSourceSpec `json:"right"`
	Deep                 bool              `json:"deep"`
	TimeToleranceSeconds int               `json:"time_tolerance_seconds,omitempty"`
	IgnoreExts           []string          `json:"ignore_exts,omitempty"`
	IgnoreDirs           []string          `json:"ignore_dirs,omitempty"`
}
type compareScanItem struct {
	RelPath string           `json:"rel_path"`
	Status  string           `json:"status"`
	Left    *comparefs.Entry `json:"left,omitempty"`
	Right   *comparefs.Entry `json:"right,omitempty"`
}
type compareScanSummary struct {
	Same       int `json:"same"`
	Different  int `json:"different"`
	Suspect    int `json:"suspect"`
	LeftNewer  int `json:"left_newer"`
	RightNewer int `json:"right_newer"`
	LeftOnly   int `json:"left_only"`
	RightOnly  int `json:"right_only"`
	Errors     int `json:"errors"`
}
type compareScanResult struct {
	Items     []compareScanItem  `json:"items"`
	Summary   compareScanSummary `json:"summary"`
	Truncated bool               `json:"truncated"`
	ElapsedMs int64              `json:"elapsed_ms"`
}

func (s *Server) handleCompareConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	type serverView struct {
		System   string `json:"system"`
		Name     string `json:"name"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
	}
	views := make([]serverView, 0)
	for _, system := range s.cur().Systems {
		for _, server := range system.Servers {
			views = append(views, serverView{System: system.Name, Name: server.Name, Host: server.Host, Port: server.Port, Username: server.Username})
		}
	}
	writeJSON(w, 200, map[string]any{"sftp": views, "kinds": []string{"local", "sftp", "ftp"}})
}

func (s *Server) openCompareFS(ctx context.Context, spec compareSourceSpec) (comparefs.FS, error) {
	switch strings.ToLower(strings.TrimSpace(spec.Kind)) {
	case "", "local":
		if !s.cur().App.ComparePathAllowed(spec.Path) {
			return nil, errors.New("路径不在 compare_allowed_roots 白名单内")
		}
		return comparefs.NewLocal(), nil
	case "sftp":
		cur := s.cur()
		_, srv, ok := cur.FindServer(spec.System, spec.Server)
		if !ok {
			return nil, errors.New("SFTP 系统或服务器不存在")
		}
		creds, err := s.resolveCreds(spec.Username, spec.Password, spec.System, spec.Server, srv.Username, srv.Password)
		if err != nil || creds.Password == "" {
			return nil, errors.New("SFTP 凭据不可用，请先在 SSH 终端保存凭据")
		}
		dialCtx, cancel := context.WithTimeout(ctx, sshDialOuterTimeout)
		defer cancel()
		cli, err := sshclient.Dial(dialCtx, sshclient.Server{Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: creds.Username, HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile, AllowInsecureHostKey: cur.App.AllowInsecureHostKeyEnabled()}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
		if err != nil {
			return nil, fmt.Errorf("SSH 连接失败: %w", err)
		}
		runFn := func(c context.Context, cmd string, timeout time.Duration, encoding string) (string, string, int, error) {
			return cli.Run(c, cmd, timeout, encoding)
		}
		sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
		if err != nil {
			_ = cli.Close()
			return nil, err
		}
		return comparefs.NewSFTP(sftpCli, cli, spec.System+" / "+spec.Server), nil
	case "ftp", "ftps":
		if strings.ContainsAny(spec.Host, "\x00\r\n") || strings.TrimSpace(spec.Host) == "" {
			return nil, errors.New("FTP 主机不能为空或包含非法字符")
		}
		return comparefs.DialFTP(ctx, comparefs.FTPConfig{Host: spec.Host, Port: spec.Port, Username: spec.Username, Password: spec.Password, TLSMode: spec.TLSMode, InsecureSkipVerify: spec.InsecureSkipVerify})
	default:
		return nil, fmt.Errorf("不支持的数据源类型: %s", spec.Kind)
	}
}

func (s *Server) handleCompareList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req compareListReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	fsys, err := s.openCompareFS(r.Context(), req.Source)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	defer fsys.Close()
	entries, err := fsys.List(r.Context(), req.Source.Path)
	if err != nil {
		writeErrSanitized(w, 400, err)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	writeJSON(w, 200, map[string]any{"entries": entries, "source_name": fsys.DisplayName()})
}

func (s *Server) handleCompareRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req compareReadReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	limit := req.MaxBytes
	if limit <= 0 || limit > compareReadLimit {
		limit = compareReadLimit
	}
	fsys, err := s.openCompareFS(r.Context(), req.Source)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	defer fsys.Close()
	entry, err := fsys.Stat(r.Context(), req.Source.Path)
	if err != nil || entry.IsDir {
		writeErr(w, 400, errors.New("目标不是可读取文件"))
		return
	}
	reader, err := fsys.Open(r.Context(), req.Source.Path)
	if err != nil {
		writeErrSanitized(w, 400, err)
		return
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		writeErrSanitized(w, 400, err)
		return
	}
	truncated := int64(len(raw)) > limit
	if truncated {
		writeJSON(w, 200, compareReadResp{Entry: entry, Version: entry.Version(), Truncated: true, Encoding: req.Source.Encoding})
		return
	}
	text, info, binary, decodeErr := textcodec.Decode(raw, req.Source.Encoding)
	if decodeErr != nil {
		writeErr(w, 400, fmt.Errorf("文本解码失败: %w", decodeErr))
		return
	}
	writeJSON(w, 200, compareReadResp{Text: text, Entry: entry, Version: entry.Version(), Truncated: truncated, Binary: binary, Encoding: info.Encoding, EOL: info.EOL, BOM: info.BOM})
}

func (s *Server) handleCompareWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req compareWriteReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, compareReadLimit+128*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	fsys, err := s.openCompareFS(r.Context(), req.Target)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	defer fsys.Close()
	encoded, err := textcodec.Encode(req.Content, textcodec.Info{Encoding: req.Encoding, EOL: req.EOL, BOM: req.BOM})
	if err != nil {
		writeErr(w, 400, fmt.Errorf("文本编码失败: %w", err))
		return
	}
	if err := fsys.WriteAtomic(r.Context(), req.Target.Path, bytes.NewReader(encoded), comparefs.WriteOptions{Expected: req.Expected, Backup: req.Backup}); err != nil {
		if errors.Is(err, comparefs.ErrConflict) {
			writeErr(w, 409, errors.New("目标文件已变化，请重新比较后再保存"))
			return
		}
		writeErrSanitized(w, 400, err)
		return
	}
	s.audit.Write("compare.write", "kind", req.Target.Kind, "path", req.Target.Path, "bytes", len(encoded), "encoding", req.Encoding, "backup", req.Backup)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleCompareCopy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req compareCopyReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	source, err := s.openCompareFS(r.Context(), req.Source)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	defer source.Close()
	target, err := s.openCompareFS(r.Context(), req.Target)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	defer target.Close()
	entry, err := source.Stat(r.Context(), req.Source.Path)
	if err != nil || entry.IsDir {
		writeErr(w, 400, errors.New("源必须是文件"))
		return
	}
	reader, err := source.Open(r.Context(), req.Source.Path)
	if err != nil {
		writeErrSanitized(w, 400, err)
		return
	}
	defer reader.Close()
	targetDir := filepath.Dir(req.Target.Path)
	if req.Target.Kind != "local" && req.Target.Kind != "" {
		targetDir = path.Dir(req.Target.Path)
	}
	if err := target.MkdirAll(r.Context(), target.Clean(targetDir), 0o755); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := target.WriteAtomic(r.Context(), req.Target.Path, reader, comparefs.WriteOptions{Expected: req.Expected, Mode: entry.Mode, Backup: req.Backup, ModTime: entry.ModTime}); err != nil {
		if errors.Is(err, comparefs.ErrConflict) {
			writeErr(w, 409, errors.New("目标文件已变化，请重新扫描后再复制"))
			return
		}
		writeErrSanitized(w, 400, err)
		return
	}
	s.audit.Write("compare.copy", "from_kind", req.Source.Kind, "from", req.Source.Path, "to_kind", req.Target.Kind, "to", req.Target.Path, "bytes", entry.Size)
	writeJSON(w, 200, map[string]any{"ok": true, "bytes": entry.Size})
}

func (s *Server) handleCompareSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req compareSyncReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := validateCompareSyncReq(req); err != nil {
		writeErr(w, 400, err)
		return
	}
	result, err := s.performCompareSync(r.Context(), req, nil)
	if err != nil {
		writeErrSanitized(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": result.Failed == 0, "copied": result.Copied, "failed": result.Failed, "bytes": result.Bytes, "failures": result.Failures})
}

func (s *Server) handleCompareSyncStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req compareSyncReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := validateCompareSyncReq(req); err != nil {
		writeErr(w, 400, err)
		return
	}
	job, ctx := s.compares.create(context.Background())
	go s.runCompareSync(ctx, job, req)
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": job.ID})
}

func validateCompareSyncReq(req compareSyncReq) error {
	if len(req.Items) == 0 || len(req.Items) > 5000 {
		return errors.New("每次同步需选择 1 到 5000 个文件")
	}
	if req.Direction != "right" && req.Direction != "left" {
		return errors.New("同步方向必须是 left 或 right")
	}
	return nil
}

func (s *Server) performCompareSync(ctx context.Context, req compareSyncReq, progress func(current, total int, message string)) (*compareSyncResult, error) {
	sourceSpec, targetSpec := req.Left, req.Right
	if req.Direction == "left" {
		sourceSpec, targetSpec = req.Right, req.Left
	}
	source, err := s.openCompareFS(ctx, sourceSpec)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	target, err := s.openCompareFS(ctx, targetSpec)
	if err != nil {
		return nil, err
	}
	defer target.Close()

	result := &compareSyncResult{Failures: make([]compareSyncFailure, 0)}
	for index, item := range req.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if progress != nil {
			progress(index, len(req.Items), "正在同步 "+item.RelPath)
		}
		rel := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(item.RelPath, "\\", "/")), "/")
		if rel == "" || rel == "." || strings.HasPrefix(rel, "../") {
			result.Failures = append(result.Failures, compareSyncFailure{RelPath: item.RelPath, Error: "非法相对路径"})
			continue
		}
		sourcePath := source.Join(sourceSpec.Path, rel)
		targetPath := target.Join(targetSpec.Path, rel)
		entry, statErr := source.Stat(ctx, sourcePath)
		if statErr != nil || entry.IsDir {
			result.Failures = append(result.Failures, compareSyncFailure{RelPath: item.RelPath, Error: "源文件不存在或不是文件"})
			continue
		}
		reader, openErr := source.Open(ctx, sourcePath)
		if openErr != nil {
			result.Failures = append(result.Failures, compareSyncFailure{RelPath: item.RelPath, Error: "打开源文件失败"})
			continue
		}
		targetDir := filepath.Dir(targetPath)
		if target.Kind() != "local" {
			targetDir = path.Dir(targetPath)
		}
		writeErrValue := target.MkdirAll(ctx, targetDir, 0o755)
		if writeErrValue == nil {
			writeErrValue = target.WriteAtomic(ctx, targetPath, reader, comparefs.WriteOptions{Expected: item.Expected, Mode: entry.Mode, Backup: req.Backup, ModTime: entry.ModTime})
		}
		_ = reader.Close()
		if writeErrValue != nil {
			if errors.Is(writeErrValue, context.Canceled) {
				return nil, writeErrValue
			}
			message := "复制失败"
			if errors.Is(writeErrValue, comparefs.ErrConflict) {
				message = "目标已变化，请重新扫描"
			}
			result.Failures = append(result.Failures, compareSyncFailure{RelPath: item.RelPath, Error: message})
			continue
		}
		result.Copied++
		result.Bytes += entry.Size
	}
	result.Failed = len(result.Failures)
	if progress != nil {
		progress(len(req.Items), len(req.Items), "同步完成")
	}
	s.audit.Write("compare.sync", "direction", req.Direction, "copied", result.Copied, "failed", result.Failed, "bytes", result.Bytes)
	return result, nil
}

func (s *Server) runCompareSync(ctx context.Context, job *compareJob, req compareSyncReq) {
	result, err := s.performCompareSync(ctx, req, func(current, total int, message string) {
		job.progress("syncing", current, total, message)
	})
	job.mu.Lock()
	defer job.mu.Unlock()
	job.Updated = time.Now()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			job.Status = "cancelled"
			job.Message = "同步已取消"
		} else {
			job.Status = "failed"
			job.Error = err.Error()
		}
		return
	}
	job.Status = "completed"
	job.Phase = "done"
	job.Message = "同步完成"
	job.Current = len(req.Items)
	job.Total = len(req.Items)
	job.SyncResult = result
}

func (s *Server) handleCompareScanStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req compareScanReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	job, ctx := s.compares.create(context.Background())
	go s.runCompareScan(ctx, job, req)
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": job.ID})
}

func (s *Server) handleCompareJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/compare/jobs/")
	job, ok := s.compares.get(id)
	if !ok {
		writeErr(w, 404, errors.New("比对任务不存在或已过期"))
		return
	}
	if r.Method == http.MethodDelete {
		job.cancel()
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET/DELETE"))
		return
	}
	writeJSON(w, 200, job.view())
}

func (s *Server) runCompareScan(ctx context.Context, job *compareJob, req compareScanReq) {
	started := time.Now()
	finishError := func(err error) {
		job.mu.Lock()
		defer job.mu.Unlock()
		if errors.Is(err, context.Canceled) {
			job.Status = "cancelled"
		} else {
			job.Status = "failed"
			job.Error = err.Error()
		}
		job.Updated = time.Now()
	}
	leftFS, err := s.openCompareFS(ctx, req.Left)
	if err != nil {
		finishError(err)
		return
	}
	defer leftFS.Close()
	rightFS, err := s.openCompareFS(ctx, req.Right)
	if err != nil {
		finishError(err)
		return
	}
	defer rightFS.Close()

	job.progress("scanning", 0, 0, "正在扫描两侧目录")
	var left, right map[string]comparefs.Entry
	var leftTrunc, rightTrunc bool
	var leftErr, rightErr error
	var wg sync.WaitGroup
	var discovered atomic.Int64
	reportDiscovered := func(delta int) {
		current := discovered.Add(int64(delta))
		if current == int64(delta) || current%100 == 0 {
			job.progress("scanning", int(current), 0, "正在扫描两侧目录 · 已发现 "+strconv.FormatInt(current, 10)+" 项")
		}
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		left, leftTrunc, leftErr = walkCompareFS(ctx, leftFS, req.Left.Path, req, reportDiscovered)
	}()
	go func() {
		defer wg.Done()
		right, rightTrunc, rightErr = walkCompareFS(ctx, rightFS, req.Right.Path, req, reportDiscovered)
	}()
	wg.Wait()
	if leftErr != nil {
		finishError(leftErr)
		return
	}
	if rightErr != nil {
		finishError(rightErr)
		return
	}

	paths := make([]string, 0, len(left)+len(right))
	seen := make(map[string]bool, len(left)+len(right))
	for rel := range left {
		seen[rel] = true
	}
	for rel := range right {
		seen[rel] = true
	}
	for rel := range seen {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	result := &compareScanResult{Items: make([]compareScanItem, 0, len(paths)), Truncated: leftTrunc || rightTrunc}
	job.progress("comparing", 0, len(paths), "正在比较元数据")
	for i, rel := range paths {
		if err := ctx.Err(); err != nil {
			finishError(err)
			return
		}
		l, hasL := left[rel]
		r, hasR := right[rel]
		item := compareScanItem{RelPath: rel}
		if hasL {
			copy := l
			item.Left = &copy
		}
		if hasR {
			copy := r
			item.Right = &copy
		}
		switch {
		case !hasL:
			item.Status = "right_only"
		case !hasR:
			item.Status = "left_only"
		case l.IsDir != r.IsDir:
			item.Status = "different"
		case l.IsDir:
			item.Status = "same"
		case l.Size != r.Size:
			item.Status = compareTimeStatus(l.ModTime, r.ModTime, req.TimeToleranceSeconds)
		case req.Deep:
			var lh, rh string
			var le, re error
			if leftFS.Kind() == "local" && rightFS.Kind() == "local" {
				var hashWG sync.WaitGroup
				hashWG.Add(2)
				go func() { defer hashWG.Done(); lh, le = hashCompareFile(ctx, leftFS, l.Path) }()
				go func() { defer hashWG.Done(); rh, re = hashCompareFile(ctx, rightFS, r.Path) }()
				hashWG.Wait()
			} else {
				lh, le = hashCompareFile(ctx, leftFS, l.Path)
				rh, re = hashCompareFile(ctx, rightFS, r.Path)
			}
			if le != nil || re != nil {
				item.Status = "error"
			} else if lh == rh {
				item.Status = "same"
			} else {
				item.Status = compareTimeStatus(l.ModTime, r.ModTime, req.TimeToleranceSeconds)
			}
		case compareTimesEqual(l.ModTime, r.ModTime, req.TimeToleranceSeconds):
			item.Status = "same"
		default:
			item.Status = compareTimeStatus(l.ModTime, r.ModTime, req.TimeToleranceSeconds)
		}
		addCompareSummary(&result.Summary, item.Status)
		result.Items = append(result.Items, item)
		if i%100 == 0 {
			job.progress("comparing", i, len(paths), "正在比较 "+strconv.Itoa(i)+" / "+strconv.Itoa(len(paths)))
		}
	}
	result.ElapsedMs = time.Since(started).Milliseconds()
	job.mu.Lock()
	job.Status = "completed"
	job.Phase = "done"
	job.Result = result
	job.Current = len(paths)
	job.Total = len(paths)
	job.Message = "比较完成"
	job.Updated = time.Now()
	job.mu.Unlock()
}

func compareTimesEqual(left, right time.Time, toleranceSeconds int) bool {
	if left.IsZero() || right.IsZero() {
		return left.Equal(right)
	}
	if toleranceSeconds <= 0 {
		toleranceSeconds = 2
	}
	delta := left.Sub(right)
	if delta < 0 {
		delta = -delta
	}
	return delta <= time.Duration(toleranceSeconds)*time.Second
}

func compareTimeStatus(left, right time.Time, toleranceSeconds int) string {
	if compareTimesEqual(left, right, toleranceSeconds) {
		return "different"
	}
	if left.IsZero() || right.IsZero() {
		return "different"
	}
	if left.After(right) {
		return "left_newer"
	}
	return "right_newer"
}

func addCompareSummary(summary *compareScanSummary, status string) {
	switch status {
	case "same":
		summary.Same++
	case "different":
		summary.Different++
	case "suspect":
		summary.Suspect++
	case "left_newer":
		summary.LeftNewer++
	case "right_newer":
		summary.RightNewer++
	case "left_only":
		summary.LeftOnly++
	case "right_only":
		summary.RightOnly++
	case "error":
		summary.Errors++
	}
}

func walkCompareFS(ctx context.Context, fsys comparefs.FS, root string, req compareScanReq, progress func(int)) (map[string]comparefs.Entry, bool, error) {
	result := make(map[string]comparefs.Entry)
	type queued struct{ abs, rel string }
	queue := []queued{{abs: root}}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		current := queue[0]
		queue = queue[1:]
		entries, err := fsys.List(ctx, current.abs)
		if err != nil {
			return nil, false, err
		}
		for _, entry := range entries {
			rel := entry.Name
			if current.rel != "" {
				rel = current.rel + "/" + entry.Name
			}
			if ignoreCompareEntry(rel, entry.IsDir, req.IgnoreExts, req.IgnoreDirs) {
				continue
			}
			entry.Path = fsys.Join(root, rel)
			result[rel] = entry
			if len(result) >= compareMaxFiles {
				return result, true, nil
			}
			if entry.IsDir {
				queue = append(queue, queued{abs: entry.Path, rel: rel})
			}
		}
		if progress != nil && len(entries) > 0 {
			progress(len(entries))
		}
	}
	return result, false, nil
}

func ignoreCompareEntry(rel string, isDir bool, exts, dirs []string) bool {
	base := filepath.Base(rel)
	if isDir {
		defaults := []string{".git", "node_modules", "__pycache__", "target", "dist", "build", ".idea", ".vscode"}
		for _, name := range append(defaults, dirs...) {
			if strings.EqualFold(strings.TrimSpace(name), base) {
				return true
			}
		}
		return false
	}
	// Atomic writes may leave a partial after an interrupted process, and the
	// optional overwrite backup deliberately lives beside its source. Neither is
	// user content, so never surface these implementation artifacts as diffs.
	lowerBase := strings.ToLower(base)
	if strings.Contains(lowerBase, ".kairo-backup-") || (strings.Contains(lowerBase, ".kairo-") && strings.HasSuffix(lowerBase, ".partial")) {
		return true
	}
	ext := strings.ToLower(filepath.Ext(base))
	for _, candidate := range exts {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate != "" && !strings.HasPrefix(candidate, ".") {
			candidate = "." + candidate
		}
		if candidate == ext {
			return true
		}
	}
	return false
}

func hashCompareFile(ctx context.Context, fsys comparefs.FS, name string) (string, error) {
	r, err := fsys.Open(ctx, name)
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := sha256.New()
	if _, err := copyWithContext(ctx, h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 128*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, re := src.Read(buf)
		if n > 0 {
			wn, we := dst.Write(buf[:n])
			total += int64(wn)
			if we != nil {
				return total, we
			}
			if wn != n {
				return total, io.ErrShortWrite
			}
		}
		if re == io.EOF {
			return total, nil
		}
		if re != nil {
			return total, re
		}
	}
}
