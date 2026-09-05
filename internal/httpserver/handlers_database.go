package httpserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"kairo/internal/credentials"
	"kairo/internal/dbconsole"
)

const databaseBodyLimit = 2 << 20

func (s *Server) handleDatabaseDispatch(w http.ResponseWriter, r *http.Request) {
	if s.database == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("数据库工作台不可用: %v", s.databaseErr))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/database/")
	switch {
	case path == "sources":
		s.handleDatabaseSources(w, r)
	case strings.HasPrefix(path, "sources/"):
		s.handleDatabaseSourceItem(w, r, strings.TrimPrefix(path, "sources/"))
	case path == "query":
		s.handleDatabaseQuery(w, r)
	case path == "batch":
		s.handleDatabaseBatch(w, r)
	case path == "transaction":
		s.handleDatabaseTransaction(w, r)
	case path == "transaction/status":
		s.handleDatabaseTransactionStatus(w, r)
	case path == "transactions":
		s.handleDatabaseTransactionList(w, r)
	case path == "script":
		s.handleDatabaseScript(w, r)
	case path == "grid":
		s.handleDatabaseGrid(w, r)
	case path == "import/preview":
		s.handleDatabaseImportPreview(w, r)
	case path == "import/apply":
		s.handleDatabaseImportApply(w, r)
	case path == "export":
		s.handleDatabaseExport(w, r)
	case path == "metadata/schemas":
		s.handleDatabaseSchemas(w, r)
	case path == "metadata/objects":
		s.handleDatabaseObjects(w, r)
	case path == "metadata/fields":
		s.handleDatabaseFields(w, r)
	case path == "metadata/indexes":
		s.handleDatabaseIndexes(w, r)
	case path == "metadata/constraints":
		s.handleDatabaseConstraints(w, r)
	case path == "metadata/inspect":
		s.handleDatabaseInspect(w, r)
	case path == "metadata/source":
		s.handleDatabaseFunctionSource(w, r)
	case path == "compile":
		s.handleDatabaseFunctionCompileLegacy(w, r)
	case path == "ddl":
		s.handleDatabaseDDLLegacy(w, r)
	case path == "object-studio":
		s.handleDatabaseObjectStudio(w, r, "")
	case strings.HasPrefix(path, "object-studio/"):
		s.handleDatabaseObjectStudio(w, r, strings.TrimPrefix(path, "object-studio/"))
	case path == "explain":
		s.handleDatabaseExplain(w, r)
	case path == "redis/scan":
		s.handleDatabaseRedisScan(w, r)
	case path == "redis/key":
		s.handleDatabaseRedisKey(w, r)
	case path == "redis/command":
		s.handleDatabaseRedisCommand(w, r)
	case path == "redis/members":
		s.handleDatabaseRedisMembers(w, r)
	case path == "redis/info":
		s.handleDatabaseRedisInfo(w, r)
	case path == "redis/mutate":
		s.handleDatabaseRedisMutate(w, r)
	case path == "sessions/backup":
		s.handleDatabaseSessionBackup(w, r)
	case path == "sessions/restore":
		s.handleDatabaseSessionRestore(w, r)
	default:
		http.NotFound(w, r)
	}
}

type databaseSourceRequest struct {
	Source      dbconsole.Source `json:"source"`
	Password    string           `json:"password"`
	SSHPassword string           `json:"ssh_password,omitempty"`
}

func (s *Server) handleDatabaseSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		user, _ := r.Context().Value(authUserKey).(*authUser)
		items := s.database.Store().List()
		views := make([]dbconsole.SourceView, 0, len(items))
		for _, source := range items {
			if user != nil && !source.UserAllowed(user.Name, user.Role) {
				continue
			}
			has := false
			if user == nil || user.Role == "admin" {
				has, _ = credentials.HasResource(dbconsole.CredentialNamespace, source.ID, source.CredentialUser())
			}
			view := databaseSourceView(source, has, user)
			if user == nil || user.Role == "admin" {
				if source.SSHTunnel != nil && source.SSHTunnel.Enabled {
					view.HasSSHPassword, _ = credentials.HasResource(dbconsole.SSHCredentialNamespace, source.ID, source.SSHTunnel.Username)
				}
			}
			views = append(views, view)
		}
		writeJSON(w, 200, map[string]any{"ok": true, "sources": views})
	case http.MethodPost:
		if !requireAdmin(w, r) {
			return
		}
		var req databaseSourceRequest
		if err := decodeDatabaseJSON(r, &req); err != nil {
			writeErr(w, 400, err)
			return
		}
		if req.Source.ID != "" {
			writeErr(w, 400, errors.New("创建数据源时不能指定 id"))
			return
		}
		if req.Password == "" && !strings.EqualFold(strings.TrimSpace(req.Source.Kind), dbconsole.KindRedis) {
			writeErr(w, 400, errors.New("密码不能为空"))
			return
		}
		source, err := s.database.Store().Save(req.Source)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if req.Password != "" {
			if err := credentials.SaveResource(dbconsole.CredentialNamespace, source.ID, source.CredentialUser(), req.Password); err != nil {
				_, _ = s.database.Store().Delete(source.ID)
				writeErrSanitized(w, 500, err)
				return
			}
		}
		if req.Source.SSHTunnel != nil && req.Source.SSHTunnel.Enabled {
			if strings.TrimSpace(req.SSHPassword) == "" {
				_, _ = s.database.Store().Delete(source.ID)
				if req.Password != "" {
					_ = credentials.ClearResource(dbconsole.CredentialNamespace, source.ID, source.CredentialUser())
				}
				writeErr(w, 400, errors.New("启用 SSH 隧道时 ssh_password 不能为空"))
				return
			}
			if err := credentials.SaveResource(dbconsole.SSHCredentialNamespace, source.ID, source.SSHTunnel.Username, req.SSHPassword); err != nil {
				_, _ = s.database.Store().Delete(source.ID)
				if req.Password != "" {
					_ = credentials.ClearResource(dbconsole.CredentialNamespace, source.ID, source.CredentialUser())
				}
				writeErrSanitized(w, 500, err)
				return
			}
		}
		s.audit.Write("database.source.create", "source_id", source.ID, "kind", source.Kind, "name", source.Name, "result", "ok")
		view := dbconsole.SourceView{Source: source, HasPassword: req.Password != "", HasSSHPassword: source.SSHTunnel != nil && source.SSHTunnel.Enabled && req.SSHPassword != ""}
		writeJSON(w, 201, map[string]any{"ok": true, "source": view})
	default:
		writeErr(w, 405, errors.New("仅支持 GET/POST"))
	}
}

func (s *Server) handleDatabaseSourceItem(w http.ResponseWriter, r *http.Request, suffix string) {
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if len(parts) == 2 && parts[1] == "test" {
		if r.Method != http.MethodPost {
			writeErr(w, 405, errors.New("仅支持 POST"))
			return
		}
		source, ok := s.databaseSourceForRequest(w, r, id)
		if !ok {
			return
		}
		result, err := s.database.Test(r.Context(), source)
		if err != nil {
			err = s.databaseSafeError(source, err)
			s.audit.Write("database.source.test", "source_id", id, "kind", source.Kind, "result", "fail", "error", trim(err.Error(), 300))
			writeErrSanitized(w, 502, err)
			return
		}
		s.audit.Write("database.source.test", "source_id", id, "kind", source.Kind, "latency_ms", result.LatencyMS, "result", "ok")
		writeJSON(w, 200, result)
		return
	}
	if len(parts) != 1 || !requireAdmin(w, r) {
		if len(parts) != 1 {
			http.NotFound(w, r)
		}
		return
	}
	old, exists := s.database.Store().Get(id)
	if !exists {
		writeErr(w, 404, fs.ErrNotExist)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var req databaseSourceRequest
		if err := decodeDatabaseJSON(r, &req); err != nil {
			writeErr(w, 400, err)
			return
		}
		req.Source.ID = id
		req.Source.CreatedAt = old.CreatedAt
		// Normalize before deriving credential identities. Store.Save applies the
		// same defaults, and doing it here keeps the staged credential plan in
		// lock-step with the source that will be persisted.
		req.Source.Defaults()
		credentialIdentityChanged := !strings.EqualFold(strings.TrimSpace(req.Source.Kind), old.Kind) || req.Source.CredentialUser() != old.CredentialUser()
		oldTunnelUser := ""
		if old.SSHTunnel != nil && old.SSHTunnel.Enabled {
			oldTunnelUser = old.SSHTunnel.Username
		}
		newTunnelUser := ""
		if req.Source.SSHTunnel != nil && req.Source.SSHTunnel.Enabled {
			newTunnelUser = req.Source.SSHTunnel.Username
		}
		sshIdentityChanged := oldTunnelUser != newTunnelUser || old.SSHTunnel != nil && req.Source.SSHTunnel != nil && old.SSHTunnel.Host != req.Source.SSHTunnel.Host || oldTunnelUser == "" && newTunnelUser != ""
		if credentialIdentityChanged && req.Password == "" && !strings.EqualFold(strings.TrimSpace(req.Source.Kind), dbconsole.KindRedis) {
			writeErr(w, 400, errors.New("修改数据库类型或用户名时必须重新输入密码"))
			return
		}
		if newTunnelUser != "" && (sshIdentityChanged || oldTunnelUser == "") && strings.TrimSpace(req.SSHPassword) == "" {
			writeErr(w, 400, errors.New("新增或修改 SSH 隧道账号时必须重新输入 ssh_password"))
			return
		}

		oldDBKey := databaseCredentialKey{namespace: dbconsole.CredentialNamespace, resource: id, username: old.CredentialUser()}
		newDBKey := databaseCredentialKey{namespace: dbconsole.CredentialNamespace, resource: id, username: req.Source.CredentialUser()}
		oldSSHKey := databaseCredentialKey{namespace: dbconsole.SSHCredentialNamespace, resource: id, username: oldTunnelUser}
		newSSHKey := databaseCredentialKey{namespace: dbconsole.SSHCredentialNamespace, resource: id, username: newTunnelUser}
		credentialKeys := make([]databaseCredentialKey, 0, 4)
		if credentialIdentityChanged || req.Password != "" {
			credentialKeys = append(credentialKeys, oldDBKey)
			if req.Password != "" {
				credentialKeys = append(credentialKeys, newDBKey)
			}
		}
		if sshIdentityChanged || req.SSHPassword != "" {
			credentialKeys = append(credentialKeys, oldSSHKey)
			if req.SSHPassword != "" {
				credentialKeys = append(credentialKeys, newSSHKey)
			}
		}
		snapshots, err := snapshotDatabaseCredentials(credentialKeys)
		if err != nil {
			writeErrSanitized(w, 500, err)
			return
		}
		rollbackCredentials := func() {
			if rollbackErr := restoreDatabaseCredentials(snapshots); rollbackErr != nil {
				s.audit.Write("database.source.update.credential_rollback", "source_id", id, "result", "fail", "error", trim(rollbackErr.Error(), 300))
			}
		}
		// Stage every new secret first. In particular, do not clear the old DB
		// credential before the SSH SaveResource call succeeds.
		if req.Password != "" {
			if err := credentials.SaveResource(newDBKey.namespace, newDBKey.resource, newDBKey.username, req.Password); err != nil {
				rollbackCredentials()
				writeErrSanitized(w, 500, err)
				return
			}
		}
		if newTunnelUser != "" && req.SSHPassword != "" {
			if err := credentials.SaveResource(newSSHKey.namespace, newSSHKey.resource, newSSHKey.username, req.SSHPassword); err != nil {
				rollbackCredentials()
				writeErrSanitized(w, 500, err)
				return
			}
		}

		updated, err := s.database.Store().Save(req.Source)
		if err != nil {
			rollbackCredentials()
			writeErr(w, 400, err)
			return
		}
		// Remove superseded entries only after all new entries and the source
		// configuration have succeeded. A clear failure rolls back both sides.
		clearKeys := make([]databaseCredentialKey, 0, 2)
		if (credentialIdentityChanged || req.Password != "") && !oldDBKey.equal(newDBKey) {
			clearKeys = append(clearKeys, oldDBKey)
		}
		if (sshIdentityChanged || req.SSHPassword != "") && !oldSSHKey.empty() && !oldSSHKey.equal(newSSHKey) {
			clearKeys = append(clearKeys, oldSSHKey)
		}
		for _, key := range clearKeys {
			if err := clearDatabaseCredential(key); err != nil {
				if _, rollbackErr := s.database.Store().Save(old); rollbackErr != nil {
					s.audit.Write("database.source.update.rollback", "source_id", id, "result", "fail", "error", trim(rollbackErr.Error(), 300))
				}
				rollbackCredentials()
				writeErrSanitized(w, 500, err)
				return
			}
		}
		s.database.Invalidate(id)
		has, _ := credentials.HasResource(dbconsole.CredentialNamespace, id, updated.CredentialUser())
		s.audit.Write("database.source.update", "source_id", id, "kind", updated.Kind, "name", updated.Name, "result", "ok")
		hasSSH := false
		if updated.SSHTunnel != nil && updated.SSHTunnel.Enabled {
			hasSSH, _ = credentials.HasResource(dbconsole.SSHCredentialNamespace, id, updated.SSHTunnel.Username)
		}
		writeJSON(w, 200, map[string]any{"ok": true, "source": dbconsole.SourceView{Source: updated, HasPassword: has, HasSSHPassword: hasSSH}})
	case http.MethodDelete:
		deleted, err := s.database.Store().Delete(id)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		s.database.Invalidate(id)
		if err := credentials.ClearResource(dbconsole.CredentialNamespace, id, deleted.CredentialUser()); err != nil && !errors.Is(err, credentials.ErrNotSaved) && !errors.Is(err, credentials.ErrUnavailable) {
			s.audit.Write("database.source.credential.delete", "source_id", id, "result", "fail", "error", trim(err.Error(), 300))
		}
		if deleted.SSHTunnel != nil && deleted.SSHTunnel.Enabled {
			if err := credentials.ClearResource(dbconsole.SSHCredentialNamespace, id, deleted.SSHTunnel.Username); err != nil && !errors.Is(err, credentials.ErrNotSaved) && !errors.Is(err, credentials.ErrUnavailable) {
				s.audit.Write("database.source.ssh_credential.delete", "source_id", id, "result", "fail", "error", trim(err.Error(), 300))
			}
		}
		s.audit.Write("database.source.delete", "source_id", id, "kind", deleted.Kind, "name", deleted.Name, "result", "ok")
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, errors.New("仅支持 PUT/DELETE"))
	}
}

type databaseQueryRequest struct {
	SourceID   string                    `json:"source_id"`
	SessionID  string                    `json:"session_id,omitempty"`
	SQL        string                    `json:"sql"`
	Parameters []dbconsole.BindParameter `json:"parameters,omitempty"`
	Confirm    bool                      `json:"confirm,omitempty"`
	MaxRows    int                       `json:"max_rows"`
	Page       int                       `json:"page"`
	PageSize   int                       `json:"page_size"`
	CountMode  string                    `json:"count_mode"`
	Format     string                    `json:"format"`
	Table      string                    `json:"table"`
}

type databaseTransactionRequest struct {
	SourceID  string `json:"source_id"`
	SessionID string `json:"session_id"`
	Action    string `json:"action"`
}

func (s *Server) handleDatabaseTransaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req databaseTransactionRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if !validDatabaseSessionID(req.SessionID) {
		writeErr(w, 400, errors.New("session_id 无效"))
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	action := strings.ToUpper(strings.TrimSpace(req.Action))
	if action != "COMMIT" && action != "ROLLBACK" {
		writeErr(w, 400, errors.New("action 仅支持 COMMIT/ROLLBACK"))
		return
	}
	summary, err := s.database.ControlSessionTransaction(r.Context(), source, req.SessionID, action)
	if err != nil {
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	s.audit.Write("database.transaction", "source_id", source.ID, "action", action, "session_id", req.SessionID, "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true, "summary": summary})
}

func validDatabaseSessionID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == ':' {
			continue
		}
		return false
	}
	return true
}

func databaseQueryPage(req databaseQueryRequest) (int, int) {
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = req.MaxRows
	}
	return page, pageSize
}

func (s *Server) handleDatabaseQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req databaseQueryRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if req.SessionID != "" && !validDatabaseSessionID(req.SessionID) {
		writeErr(w, 400, errors.New("session_id 无效"))
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	user, _ := r.Context().Value(authUserKey).(*authUser)
	var statementInfo dbconsole.SQLStatementInfo
	if user != nil && user.Role != "admin" {
		if err := dbconsole.ValidateReadOnlySQL(source.Kind, req.SQL); err != nil {
			writeErr(w, 400, err)
			return
		}
	} else {
		var err error
		statementInfo, err = dbconsole.ValidateSQL(source.Kind, req.SQL)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if !statementInfo.IsQuery {
			if !source.MutationAllowed() {
				writeErr(w, http.StatusForbidden, errors.New("该数据源处于只读锁定状态"))
				return
			}
			if source.IsProduction() && !req.Confirm {
				writeErr(w, http.StatusBadRequest, errors.New("生产数据源写操作需要 confirm=true"))
				return
			}
			if statementInfo.Type == "DDL" && !source.DDLAllowed() {
				writeErr(w, http.StatusForbidden, errors.New("该数据源未开启 DDL 能力或处于只读锁定状态"))
				return
			}
		}
		if !statementInfo.IsQuery && req.SessionID == "" {
			writeErr(w, 400, errors.New("写语句必须绑定页签事务 session_id"))
			return
		}
	}
	if _, _, err := dbconsole.BindSQLParameters(source.Kind, req.SQL, req.Parameters); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	hash := sha256.Sum256([]byte(strings.TrimSpace(req.SQL)))
	queryID := hex.EncodeToString(hash[:8])
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	emit := func(event dbconsole.StreamEvent) error {
		if err := json.NewEncoder(w).Encode(event); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	page, pageSize := databaseQueryPage(req)
	summary, err := s.database.StreamSessionQueryPageWithParams(r.Context(), source, req.SQL, page, pageSize, req.SessionID, req.Parameters, emit)
	if err != nil {
		err = s.databaseSafeError(source, err)
		_ = emit(dbconsole.StreamEvent{Type: "error", Error: trim(err.Error(), 1000)})
		s.audit.Write("database.query", "source_id", source.ID, "kind", source.Kind, "query_id", queryID, "result", "fail", "error", trim(err.Error(), 300))
		return
	}
	s.audit.Write("database.query", "source_id", source.ID, "kind", source.Kind, "query_id", queryID,
		"rows", summary.Rows, "elapsed_ms", summary.ElapsedMS, "truncated", summary.Truncated, "result", "ok")
}

type databaseBatchRequest struct {
	SourceID   string   `json:"source_id"`
	SessionID  string   `json:"session_id,omitempty"`
	Statements []string `json:"statements"`
	Confirm    bool     `json:"confirm,omitempty"`
}

func (s *Server) handleDatabaseBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req databaseBatchRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if !validDatabaseSessionID(req.SessionID) {
		writeErr(w, 400, errors.New("session_id 无效"))
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	if !source.MutationAllowed() {
		writeErr(w, http.StatusForbidden, errors.New("该数据源处于只读锁定状态"))
		return
	}
	if source.IsProduction() && !req.Confirm {
		writeErr(w, http.StatusBadRequest, errors.New("生产数据源批量写入需要 confirm=true"))
		return
	}
	user, _ := r.Context().Value(authUserKey).(*authUser)
	if user != nil && user.Role != "admin" {
		writeErr(w, 403, errors.New("批量提交仅限管理员"))
		return
	}
	if len(req.Statements) == 0 {
		writeErr(w, 400, errors.New("statements 不能为空"))
		return
	}
	if len(req.Statements) > 50 {
		writeErr(w, 400, errors.New("批量语句数量超出限制 (最多 50 条)"))
		return
	}
	for _, stmt := range req.Statements {
		if _, err := dbconsole.ValidateSQL(source.Kind, stmt); err != nil {
			writeErr(w, 400, err)
			return
		}
	}
	summary, err := s.database.ExecuteSessionBatch(r.Context(), source, req.SessionID, req.Statements)
	if err != nil {
		err = s.databaseSafeError(source, err)
		writeErrSanitized(w, 502, err)
		s.audit.Write("database.batch", "source_id", source.ID, "kind", source.Kind, "statements", len(req.Statements), "result", "fail", "error", trim(err.Error(), 300))
		return
	}
	s.audit.Write("database.batch", "source_id", source.ID, "kind", source.Kind, "statements", len(req.Statements), "rows_affected", summary.RowsAffected, "elapsed_ms", summary.ElapsedMS, "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true, "summary": summary})
}

func (s *Server) handleDatabaseExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req databaseQueryRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	user, _ := r.Context().Value(authUserKey).(*authUser)
	var statementInfo dbconsole.SQLStatementInfo
	var err error
	if user != nil && user.Role != "admin" {
		if err := dbconsole.ValidateReadOnlySQL(source.Kind, req.SQL); err != nil {
			writeErr(w, 400, err)
			return
		}
	} else {
		statementInfo, err = dbconsole.ValidateSQL(source.Kind, req.SQL)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if !statementInfo.IsQuery {
			if !source.MutationAllowed() {
				writeErr(w, http.StatusForbidden, errors.New("该数据源处于只读锁定状态"))
				return
			}
			if source.IsProduction() && !req.Confirm {
				writeErr(w, http.StatusBadRequest, errors.New("生产数据源写操作需要 confirm=true"))
				return
			}
			if statementInfo.Type == "DDL" && !source.DDLAllowed() {
				writeErr(w, http.StatusForbidden, errors.New("该数据源未开启 DDL 能力或处于只读锁定状态"))
				return
			}
			// Export has no transaction lifecycle and must not become an
			// alternate autocommit write endpoint.
			writeErr(w, http.StatusBadRequest, errors.New("导出接口仅支持查询语句"))
			return
		}
	}
	hash := sha256.Sum256([]byte(strings.TrimSpace(req.SQL)))
	queryID := hex.EncodeToString(hash[:8])
	format, err := dbconsole.NormalizeExportFormat(req.Format)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	safeName := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) || r < 0x20 {
			return '_'
		}
		return r
	}, source.Name)
	if safeName == "" {
		safeName = "query"
	}
	filename := safeName + dbconsole.ExportExtension(format)
	if format != "csv" {
		page, pageSize := databaseQueryPage(req)
		table, summary, collectErr := s.database.CollectQueryPage(r.Context(), source, req.SQL, page, pageSize)
		if collectErr != nil {
			collectErr = s.databaseSafeError(source, collectErr)
			writeErrSanitized(w, 502, collectErr)
			s.audit.Write("database.export", "source_id", source.ID, "kind", source.Kind, "query_id", queryID, "format", format, "result", "fail", "error", trim(collectErr.Error(), 300))
			return
		}
		w.Header().Set("Content-Type", dbconsole.ExportContentType(format))
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.QueryEscape(filename))
		w.Header().Set("Cache-Control", "no-store")
		switch format {
		case "json":
			err = dbconsole.WriteJSON(w, table, map[string]any{"source": source.Name, "kind": source.Kind, "sql": req.SQL})
		case "xlsx":
			err = dbconsole.WriteXLSX(w, table, source.Name)
		case "insert":
			tableName := strings.TrimSpace(req.Table)
			if tableName == "" {
				tableName = dbconsole.InferExportTable(req.SQL)
			}
			err = dbconsole.WriteINSERT(w, table, source.Kind, tableName)
		case "update":
			tableName := strings.TrimSpace(req.Table)
			if tableName == "" {
				tableName = dbconsole.InferExportTable(req.SQL)
			}
			var pkCols []string
			schema, obj := dbconsole.SplitSchemaObject(tableName)
			if obj != "" {
				fields, errFields := s.database.Fields(r.Context(), source, schema, obj)
				if errFields == nil {
					for _, f := range fields {
						if f.PrimaryKey {
							pkCols = append(pkCols, f.Name)
						}
					}
				}
			}
			err = dbconsole.WriteUPDATE(w, table, source.Kind, tableName, pkCols)
		}
		if err != nil {
			s.audit.Write("database.export", "source_id", source.ID, "kind", source.Kind, "query_id", queryID, "format", format, "result", "fail", "error", trim(err.Error(), 300))
			return
		}
		s.audit.Write("database.export", "source_id", source.ID, "kind", source.Kind, "query_id", queryID, "format", format,
			"rows", summary.Rows, "elapsed_ms", summary.ElapsedMS, "truncated", summary.Truncated, "result", "ok")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.QueryEscape(filename))
	w.Header().Set("Cache-Control", "no-store")
	csvWriter := csv.NewWriter(w)
	started := false
	columnCount := 0
	emit := func(event dbconsole.StreamEvent) error {
		switch event.Type {
		case "meta":
			if _, err := io.WriteString(w, "\ufeff"); err != nil {
				return err
			}
			columnCount = len(event.Columns)
			record := make([]string, columnCount)
			for i, column := range event.Columns {
				record[i] = safeCSVCell(column.Name)
			}
			if err := csvWriter.Write(record); err != nil {
				return err
			}
			started = true
		case "rows":
			for _, row := range event.Rows {
				record := make([]string, len(row))
				for i, value := range row {
					record[i] = safeCSVCell(databaseCSVValue(value))
				}
				if err := csvWriter.Write(record); err != nil {
					return err
				}
			}
		}
		csvWriter.Flush()
		if err := csvWriter.Error(); err != nil {
			return err
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	}
	page, pageSize := databaseQueryPage(req)
	summary, err := s.database.StreamQueryPage(r.Context(), source, req.SQL, page, pageSize, emit)
	if err != nil {
		err = s.databaseSafeError(source, err)
		if !started {
			writeErrSanitized(w, 502, err)
		} else {
			record := make([]string, max(columnCount, 1))
			record[0] = "导出中断: " + trim(err.Error(), 500)
			_ = csvWriter.Write(record)
			csvWriter.Flush()
		}
		s.audit.Write("database.export", "source_id", source.ID, "kind", source.Kind, "query_id", queryID, "format", format, "result", "fail", "error", trim(err.Error(), 300))
		return
	}
	s.audit.Write("database.export", "source_id", source.ID, "kind", source.Kind, "query_id", queryID, "format", format,
		"rows", summary.Rows, "elapsed_ms", summary.ElapsedMS, "truncated", summary.Truncated, "result", "ok")
}

func databaseCSVValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	if obj, ok := value.(map[string]any); ok {
		if kind, _ := obj["kind"].(string); kind == "text" {
			if preview, ok := obj["preview"].(string); ok {
				return preview
			}
		}
	}
	if raw, err := json.Marshal(value); err == nil {
		return string(raw)
	}
	return fmt.Sprint(value)
}

func safeCSVCell(value string) string {
	if value == "" {
		return value
	}
	// Keep database text intact, including paths like /opt/app/config.
	// Only prefix Excel-executable formula starters; do not rewrite /, -, or digits.
	switch value[0] {
	case '=', '+', '@':
		return "'" + value
	case '\t', '\r':
		return "'" + value
	default:
		return value
	}
}

func (s *Server) handleDatabaseSchemas(w http.ResponseWriter, r *http.Request) {
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("refresh") == "1" {
		s.database.InvalidateMetadata(source.ID)
	}
	items, err := s.database.Schemas(r.Context(), source)
	if err != nil {
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "schemas": items})
}

func (s *Server) handleDatabaseObjects(w http.ResponseWriter, r *http.Request) {
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	items, err := s.database.Objects(r.Context(), source, r.URL.Query().Get("schema"), r.URL.Query().Get("search"))
	if err != nil {
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "objects": items})
}

func (s *Server) handleDatabaseFields(w http.ResponseWriter, r *http.Request) {
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	items, err := s.database.Fields(r.Context(), source, r.URL.Query().Get("schema"), r.URL.Query().Get("object"))
	if err != nil {
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "fields": items})
}

func (s *Server) handleDatabaseIndexes(w http.ResponseWriter, r *http.Request) {
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	items, err := s.database.Indexes(r.Context(), source, r.URL.Query().Get("schema"), r.URL.Query().Get("object"))
	if err != nil {
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "indexes": items})
}

func (s *Server) handleDatabaseConstraints(w http.ResponseWriter, r *http.Request) {
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	items, err := s.database.Constraints(r.Context(), source, r.URL.Query().Get("schema"), r.URL.Query().Get("object"))
	if err != nil {
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "constraints": items})
}

func (s *Server) handleDatabaseInspect(w http.ResponseWriter, r *http.Request) {
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	item, err := s.database.InspectObject(r.Context(), source, r.URL.Query().Get("schema"), r.URL.Query().Get("object"), r.URL.Query().Get("type"))
	if err != nil {
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "inspect": item})
}

func (s *Server) handleDatabaseExplain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req databaseQueryRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	if err := dbconsole.ValidateReadOnlySQL(source.Kind, req.SQL); err != nil {
		writeErr(w, 400, err)
		return
	}
	rows, err := s.database.Explain(r.Context(), source, req.SQL)
	if err != nil {
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	s.audit.Write("database.explain", "source_id", source.ID, "kind", source.Kind, "result", "ok", "rows", len(rows))
	writeJSON(w, 200, map[string]any{"ok": true, "plan": rows})
}

func (s *Server) handleDatabaseRedisScan(w http.ResponseWriter, r *http.Request) {
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	count, _ := strconv.ParseInt(r.URL.Query().Get("count"), 10, 64)
	result, err := s.database.RedisScan(r.Context(), source, r.URL.Query().Get("cursor"), r.URL.Query().Get("pattern"), count)
	if err != nil {
		if strings.Contains(err.Error(), "cursor 无效") {
			writeErr(w, 400, err)
			return
		}
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "result": result})
}

func (s *Server) handleDatabaseRedisKey(w http.ResponseWriter, r *http.Request) {
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	encodedKey := r.URL.Query().Get("key_base64")
	rawKey, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(rawKey) == 0 {
		writeErr(w, 400, errors.New("key_base64 必须是非空的有效 Base64"))
		return
	}
	result, err := s.database.RedisGet(r.Context(), source, string(rawKey))
	if err != nil {
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "result": result})
}

type redisCommandRequest struct {
	SourceID  string   `json:"source_id"`
	Command   string   `json:"command"`
	Key       string   `json:"key"`
	KeyBase64 string   `json:"key_base64"`
	Args      []string `json:"args"`
}

func (s *Server) handleDatabaseRedisCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req redisCommandRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	key := req.Key
	if req.KeyBase64 != "" {
		raw, decodeErr := base64.StdEncoding.DecodeString(req.KeyBase64)
		if decodeErr != nil {
			writeErr(w, 400, errors.New("key_base64 无效"))
			return
		}
		key = string(raw)
	}
	result, err := s.database.RedisReadOnlyCommand(r.Context(), source, req.Command, key, req.Args)
	if err != nil {
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	s.audit.Write("database.redis.command", "source_id", source.ID, "command", strings.ToUpper(strings.TrimSpace(req.Command)), "result", "ok", "elapsed_ms", result.ElapsedMS)
	writeJSON(w, 200, map[string]any{"ok": true, "result": result})
}

func (s *Server) handleDatabaseRedisMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	encodedKey := r.URL.Query().Get("key_base64")
	rawKey, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(rawKey) == 0 {
		writeErr(w, 400, errors.New("key_base64 必须是非空的有效 Base64"))
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	pageSize, _ := strconv.ParseInt(r.URL.Query().Get("page_size"), 10, 64)
	result, err := s.database.RedisMembers(r.Context(), source, string(rawKey), r.URL.Query().Get("type"), r.URL.Query().Get("cursor"), offset, pageSize)
	if err != nil {
		if strings.Contains(err.Error(), "cursor 无效") || strings.Contains(err.Error(), "偏移量过大") || strings.Contains(err.Error(), "不支持") {
			writeErr(w, 400, err)
			return
		}
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "result": result})
}

func (s *Server) handleDatabaseRedisInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	sections := make([]string, 0)
	for _, value := range strings.Split(r.URL.Query().Get("section"), ",") {
		if strings.TrimSpace(value) != "" {
			sections = append(sections, value)
		}
	}
	result, err := s.database.RedisInfo(r.Context(), source, sections)
	if err != nil {
		writeErrSanitized(w, 502, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "result": result})
}

type redisMutateRequest struct {
	SourceID  string `json:"source_id"`
	Key       string `json:"key"`
	KeyBase64 string `json:"key_base64"`
	Operation string `json:"operation"`
	Seconds   int64  `json:"seconds"`
	Confirm   bool   `json:"confirm"`
}

func (s *Server) handleDatabaseRedisMutate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req redisMutateRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if !req.Confirm {
		writeErr(w, 400, errors.New("受控 Redis 写操作需要二次确认"))
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	key := req.Key
	if req.KeyBase64 != "" {
		raw, decodeErr := base64.StdEncoding.DecodeString(req.KeyBase64)
		if decodeErr != nil {
			writeErr(w, 400, errors.New("key_base64 无效"))
			return
		}
		key = string(raw)
	}
	result, err := s.database.RedisMutateTTL(r.Context(), source, key, req.Operation, req.Seconds)
	if err != nil {
		writeErrSanitized(w, 400, s.databaseSafeError(source, err))
		return
	}
	s.audit.Write("database.redis.mutate", "source_id", source.ID, "operation", strings.ToUpper(req.Operation), "key_bytes", len(key), "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true, "result": result})
}

func (s *Server) databaseSourceFromQuery(w http.ResponseWriter, r *http.Request) (dbconsole.Source, bool) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return dbconsole.Source{}, false
	}
	return s.databaseSourceForRequest(w, r, r.URL.Query().Get("source_id"))
}

func (s *Server) databaseSourceForRequest(w http.ResponseWriter, r *http.Request, id string) (dbconsole.Source, bool) {
	source, ok := s.database.Store().Get(strings.TrimSpace(id))
	if !ok {
		writeErr(w, 404, errors.New("数据源不存在"))
		return dbconsole.Source{}, false
	}
	user, _ := r.Context().Value(authUserKey).(*authUser)
	if user != nil && !source.UserAllowed(user.Name, user.Role) {
		writeErr(w, 403, errors.New("没有访问该数据源的权限"))
		return dbconsole.Source{}, false
	}
	return source, true
}

func decodeDatabaseJSON(r *http.Request, target any) error {
	return decodeDatabaseJSONLimit(r, target, databaseBodyLimit)
}

func decodeDatabaseJSONLimit(r *http.Request, target any, limit int64) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, limit))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("请求体只能包含一个 JSON 值")
		}
		return err
	}
	return nil
}

func databaseSourceView(source dbconsole.Source, hasPassword bool, user *authUser) dbconsole.SourceView {
	if user != nil && user.Role != "admin" {
		source.Host = ""
		source.Port = 0
		source.Username = ""
		source.Database = ""
		source.OracleConnectBy = ""
		source.OracleService = ""
		source.OracleClientCharset = ""
		source.RedisDB = 0
		source.TLSMode = ""
		source.TLSCAFile = ""
		source.TLSClientCertFile = ""
		source.TLSClientKeyFile = ""
		source.TLSServerName = ""
		source.SSHTunnel = nil
		source.MaxResultBytes = 0
		source.MaxOpenConnections = 0
		source.MaxIdleConnections = 0
		source.ConnectionMaxMinutes = 0
		source.AllowedUsers = nil
		source.CreatedAt = ""
		source.UpdatedAt = ""
		hasPassword = false
	}
	return dbconsole.SourceView{Source: source, HasPassword: hasPassword}
}

// databaseSafeError removes every representation of the stored secret before
// a driver error reaches the browser or audit log. Some drivers include a DSN
// in connection errors, and a DSN can contain a URL-escaped password.
// 额外将常见 i/o timeout 翻译为用户友好文案，避免直接暴露 read tcp ... 细节。
func (s *Server) databaseSafeError(source dbconsole.Source, err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if secret, getErr := credentials.GetResource(dbconsole.CredentialNamespace, source.ID, source.CredentialUser()); getErr == nil && secret != "" {
		for _, value := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
			if value != "" {
				message = strings.ReplaceAll(message, value, "[REDACTED]")
			}
		}
	}
	// 将底层网络超时翻译为友好提示，隐藏内部 IP 细节
	lower := strings.ToLower(message)
	if strings.Contains(lower, "i/o timeout") || strings.Contains(lower, "io timeout") || strings.Contains(lower, "context deadline exceeded") {
		return errors.New("连接数据库超时（i/o timeout），请检查数据库地址、端口与网络连通性，或稍后重试")
	}
	if strings.Contains(lower, "connection refused") || strings.Contains(lower, "connection reset") {
		return errors.New("无法连接数据库（connection refused），请检查数据库是否启动及端口是否正确")
	}
	return errors.New(message)
}

type databaseSessionBackupPayload struct {
	ActiveID      any              `json:"activeId"`
	TabSeq        int              `json:"tabSeq"`
	SourceID      string           `json:"sourceId"`
	EditorHeight  int              `json:"editorHeight"`
	MetaCollapsed *bool            `json:"metaCollapsed"`
	Sessions      []map[string]any `json:"sessions"`
	UpdatedAt     int64            `json:"updatedAt"`
}

func databaseSessionFileName(r *http.Request) string {
	if user, _ := r.Context().Value(authUserKey).(*authUser); user != nil && strings.TrimSpace(user.Name) != "" {
		hash := sha256.Sum256([]byte(strings.TrimSpace(user.Name)))
		return fmt.Sprintf("database-sessions-%s.json", hex.EncodeToString(hash[:8]))
	}
	return "database-sessions.json"
}

func (s *Server) handleDatabaseSessionBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, databaseBodyLimit))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var payload databaseSessionBackupPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		writeErr(w, 400, fmt.Errorf("非法会话备份格式: %w", err))
		return
	}
	if len(payload.Sessions) > 50 {
		writeErr(w, 400, errors.New("页签数量超出限制 (最多 50 个)"))
		return
	}
	// 逐 session 校验：SQL 长度、sourceId 合法性、page 范围
	for _, sessMap := range payload.Sessions {
		if sqlVal, ok := sessMap["sql"].(string); ok && len(sqlVal) > 500000 {
			writeErr(w, 400, errors.New("单条 SQL 超过 500000 字符限制"))
			return
		}
		if srcID, ok := sessMap["sourceId"].(string); ok && srcID != "" {
			if len(srcID) > 128 {
				writeErr(w, 400, errors.New("sourceId 非法"))
				return
			}
		}
	}
	if payload.EditorHeight < 0 || payload.EditorHeight > 4000 {
		payload.EditorHeight = 0
	}
	filePath := filepath.Join(s.cur().DataDir(), databaseSessionFileName(r))
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		writeErrSanitized(w, 500, err)
		return
	}
	user, _ := r.Context().Value(authUserKey).(*authUser)
	userName := ""
	if user != nil {
		userName = user.Name
	}
	s.audit.Write("database.session.backup", "user", userName, "file", filepath.Base(filePath), "sessions", len(payload.Sessions), "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleDatabaseSessionRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	fileName := databaseSessionFileName(r)
	filePath := filepath.Join(s.cur().DataDir(), fileName)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if fileName != "database-sessions.json" {
			filePath = filepath.Join(s.cur().DataDir(), "database-sessions.json")
			data, err = os.ReadFile(filePath)
		}
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, 200, map[string]any{"ok": true, "session": nil})
				return
			}
			writeErrSanitized(w, 500, err)
			return
		}
	}
	// 校验文件大小与 JSON 合法性，防止异常文件导致前端崩溃
	if len(data) > databaseBodyLimit {
		writeJSON(w, 200, map[string]any{"ok": true, "session": nil})
		return
	}
	var session any
	if err := json.Unmarshal(data, &session); err != nil {
		writeJSON(w, 200, map[string]any{"ok": true, "session": nil})
		return
	}
	// 对恢复的 session 做轻量校验：若 sessions >50 则视为异常
	if m, ok := session.(map[string]any); ok {
		if sessArr, ok := m["sessions"].([]any); ok && len(sessArr) > 50 {
			writeJSON(w, 200, map[string]any{"ok": true, "session": nil})
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "session": session})
}
