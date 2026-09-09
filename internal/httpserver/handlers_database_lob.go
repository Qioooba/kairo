package httpserver

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"kairo/internal/dbconsole"
)

type databaseLobRequest struct {
	SourceID   string         `json:"source_id"`
	SessionID  string         `json:"session_id,omitempty"`
	Owner      string         `json:"owner"`
	Table      string         `json:"table"`
	Column     string         `json:"column"`
	ColumnType string         `json:"column_type,omitempty"`
	RowID      string         `json:"rowid,omitempty"`
	UseRowID   bool           `json:"use_rowid,omitempty"`
	Keys       map[string]any `json:"keys,omitempty"`
	Token      string         `json:"token,omitempty"`
	PrimaryKey []string       `json:"primary_key,omitempty"`
}

// POST /api/database/lob  流式下载完整 LOB（结论 2.4/2.6/3.3：按主键/ROWID 重查，绑定变量，流式输出）
func (s *Server) handleDatabaseLob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	// 权限：沿用数据源授权（databaseSourceForRequest 会校验 UserAllowed），
	// 不强制 admin，避免只读用户无法查看授权表的 LOB（结论 2.6：校验对象权限后放行）
	var req databaseLobRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	payload, signedRef, err := dbconsole.VerifySignedLOBToken(req.Token)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("无效或已过期的 LOB Token: %w", err))
		return
	}
	req.SourceID, req.Owner, req.Table, req.Column = payload.SourceID, payload.Owner, payload.Table, payload.Column
	req.ColumnType, req.SessionID = payload.ColumnType, payload.SessionID
	if scope := databaseSessionScope(r); req.SessionID != "" && scope != "" && !strings.HasPrefix(req.SessionID, scope) {
		writeErr(w, http.StatusForbidden, errors.New("LOB 事务不属于当前用户，请重新查询"))
		return
	}

	if strings.TrimSpace(req.SourceID) == "" || strings.TrimSpace(req.Owner) == "" || strings.TrimSpace(req.Table) == "" || strings.TrimSpace(req.Column) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("source_id/owner/table/column 不能为空（或提供有效 token）"))
		return
	}
	if len(req.Table) > 128 || len(req.Owner) > 128 || len(req.Column) > 128 {
		writeErr(w, http.StatusBadRequest, errors.New("表/列名过长"))
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	if source.Kind != dbconsole.KindOracle {
		writeErr(w, http.StatusBadRequest, errors.New("LOB 流仅支持 Oracle"))
		return
	}
	if req.SessionID != "" && !validDatabaseSessionID(req.SessionID) {
		writeErr(w, http.StatusBadRequest, errors.New("session_id 无效"))
		return
	}
	if payload.DatabaseUser != source.Username {
		writeErr(w, http.StatusForbidden, errors.New("数据源用户已变化，请重新查询"))
		return
	}
	ref := *signedRef
	if err := ref.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	isBlob := strings.EqualFold(ref.ColumnType, "BLOB")
	filename := fmt.Sprintf("%s_%s_%s", ref.Table, ref.Column, "lob")
	if isBlob {
		filename += ".bin"
		w.Header().Set("Content-Type", "application/octet-stream")
	} else {
		filename += ".txt"
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		// CLOB 文本防 XSS：强制下载，不内联渲染
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.QueryEscape(filename))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 写入前记录审计（不含敏感值）
	s.audit.Write("database.lob.stream", "source_id", source.ID, "owner", ref.Owner, "table", ref.Table, "column", ref.Column, "use_rowid", ref.UseRowID, "result", "start")

	// 限时：单个 LOB 最长 2 分钟，最大 64MB（manager 侧同限）
	// 交给 manager.StreamLOB 实现分块与 ctx 取消
	// 为支持 HTTP 背压，设置 Flush
	flusher, _ := w.(http.Flusher)
	// 用 pipe 式：直接写 ResponseWriter，每块 Flush
	// 若客户端断开，r.Context() 会取消，manager 侧会立即返回
	bytesWritten, err := s.streamLOBWithFlush(r, ref, source, isBlob, w, flusher)
	if err != nil {
		// 已开始写头后无法改状态码，只能截断并计审计
		s.audit.Write("database.lob.stream", "source_id", source.ID, "owner", ref.Owner, "table", ref.Table, "column", ref.Column, "result", "fail", "error", trim(err.Error(), 300))
		// 若尚未写入任何字节，可尝试写错误 JSON
		if bytesWritten == 0 {
			w.Header().Del("Content-Disposition")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		// Abort an incomplete response so fetch/blob cannot report a successful
		// download of truncated content. net/http handles this sentinel panic.
		panic(http.ErrAbortHandler)
	}
	s.audit.Write("database.lob.stream", "source_id", source.ID, "owner", ref.Owner, "table", ref.Table, "column", ref.Column, "bytes", bytesWritten, "result", "ok")
}

func (s *Server) streamLOBWithFlush(r *http.Request, ref dbconsole.LOBRef, source dbconsole.Source, _ bool, w http.ResponseWriter, flusher http.Flusher) (int64, error) {
	// 包装 Writer 以统计字节并在每块后 Flush
	cw := &countingWriter{w: w, flusher: flusher}
	err := s.database.StreamLOB(r.Context(), source, ref, cw)
	return cw.n, s.databaseSafeError(source, err)
}

type countingWriter struct {
	w       io.Writer
	flusher http.Flusher
	n       int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if c.flusher != nil {
		c.flusher.Flush()
	}
	return n, err
}

// GET /api/database/oracle/client-info?source_id=xxx
func (s *Server) handleDatabaseOracleClientInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return
	}
	sourceID := strings.TrimSpace(r.URL.Query().Get("source_id"))
	if sourceID != "" {
		if _, ok := s.databaseSourceForRequest(w, r, sourceID); !ok {
			return
		}
	} else if user, _ := r.Context().Value(authUserKey).(*authUser); user != nil && user.Role != "admin" {
		writeErr(w, http.StatusForbidden, errors.New("本机客户端探测需要管理员权限"))
		return
	}
	// 无 source_id 时返回本机全局探测（用于企业部署自检页）
	info, err := s.database.OracleClientInfo(r.Context(), sourceID)
	if err != nil {
		writeErrSanitized(w, http.StatusBadGateway, err)
		return
	}
	// 附加建议与精准诊断
	hint := ""
	if !info.Available {
		if info.PLSQLDetected && info.PLSQLBitness == "32-bit" {
			hint = "检测到本机安装了 32 位 PL/SQL Developer，其配套的 Oracle Client 亦为 32 位；由于 64 位 Windows 进程无法跨位数加载 32 位 DLL，Kairo 已自动使用纯 Go (go-ora) 驱动运行。若需开启原生 OCI 加速，建议安装 64 位 Oracle Instant Client (19c/21c)。"
		} else {
			hint = "企业版 Windows 默认 godror/OCI，需本机安装 64 位 Oracle Client（Instant Client 或完整客户端）。检测到 PL/SQL Developer 存在不代表可用：需位数一致（64 位 Kairo 对 64 位 oci.dll）、且 libDir 含完整依赖（含 VC++ Runtime）。可在 source 配置或环境变量 ORACLE_HOME/TNS_ADMIN 指定 libDir/configDir，重启后重试。"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "client": info, "hint": hint})
}
