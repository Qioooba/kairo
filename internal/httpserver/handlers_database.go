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
	case path == "explain":
		s.handleDatabaseExplain(w, r)
	case path == "redis/scan":
		s.handleDatabaseRedisScan(w, r)
	case path == "redis/key":
		s.handleDatabaseRedisKey(w, r)
	default:
		http.NotFound(w, r)
	}
}

type databaseSourceRequest struct {
	Source   dbconsole.Source `json:"source"`
	Password string           `json:"password"`
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
			views = append(views, databaseSourceView(source, has, user))
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
		s.audit.Write("database.source.create", "source_id", source.ID, "kind", source.Kind, "name", source.Name, "result", "ok")
		writeJSON(w, 201, map[string]any{"ok": true, "source": dbconsole.SourceView{Source: source, HasPassword: req.Password != ""}})
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
		credentialIdentityChanged := !strings.EqualFold(strings.TrimSpace(req.Source.Kind), old.Kind) || req.Source.CredentialUser() != old.CredentialUser()
		if credentialIdentityChanged && req.Password == "" && !strings.EqualFold(strings.TrimSpace(req.Source.Kind), dbconsole.KindRedis) {
			writeErr(w, 400, errors.New("修改数据库类型或用户名时必须重新输入密码"))
			return
		}
		updated, err := s.database.Store().Save(req.Source)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if req.Password != "" {
			if err := credentials.SaveResource(dbconsole.CredentialNamespace, id, updated.CredentialUser(), req.Password); err != nil {
				// 配置与凭据必须作为一个逻辑事务提交；凭据失败时恢复旧配置。
				if _, rollbackErr := s.database.Store().Save(old); rollbackErr != nil {
					s.audit.Write("database.source.update.rollback", "source_id", id, "result", "fail", "error", trim(rollbackErr.Error(), 300))
				}
				writeErrSanitized(w, 500, err)
				return
			}
			if old.CredentialUser() != updated.CredentialUser() {
				_ = credentials.ClearResource(dbconsole.CredentialNamespace, id, old.CredentialUser())
			}
		} else if credentialIdentityChanged {
			// 切换到无密码 Redis 时不能沿用旧数据库密码。
			_ = credentials.ClearResource(dbconsole.CredentialNamespace, id, old.CredentialUser())
		}
		s.database.Invalidate(id)
		has, _ := credentials.HasResource(dbconsole.CredentialNamespace, id, updated.CredentialUser())
		s.audit.Write("database.source.update", "source_id", id, "kind", updated.Kind, "name", updated.Name, "result", "ok")
		writeJSON(w, 200, map[string]any{"ok": true, "source": dbconsole.SourceView{Source: updated, HasPassword: has}})
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
		s.audit.Write("database.source.delete", "source_id", id, "kind", deleted.Kind, "name", deleted.Name, "result", "ok")
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, errors.New("仅支持 PUT/DELETE"))
	}
}

type databaseQueryRequest struct {
	SourceID string `json:"source_id"`
	SQL      string `json:"sql"`
	MaxRows  int    `json:"max_rows"`
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
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	if err := dbconsole.ValidateReadOnlySQL(source.Kind, req.SQL); err != nil {
		writeErr(w, 400, err)
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
	summary, err := s.database.StreamQuery(r.Context(), source, req.SQL, req.MaxRows, emit)
	if err != nil {
		err = s.databaseSafeError(source, err)
		_ = emit(dbconsole.StreamEvent{Type: "error", Error: trim(err.Error(), 1000)})
		s.audit.Write("database.query", "source_id", source.ID, "kind", source.Kind, "query_id", queryID, "result", "fail", "error", trim(err.Error(), 300))
		return
	}
	s.audit.Write("database.query", "source_id", source.ID, "kind", source.Kind, "query_id", queryID,
		"rows", summary.Rows, "elapsed_ms", summary.ElapsedMS, "truncated", summary.Truncated, "result", "ok")
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
	if err := dbconsole.ValidateReadOnlySQL(source.Kind, req.SQL); err != nil {
		writeErr(w, 400, err)
		return
	}
	hash := sha256.Sum256([]byte(strings.TrimSpace(req.SQL)))
	queryID := hex.EncodeToString(hash[:8])
	safeName := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) || r < 0x20 {
			return '_'
		}
		return r
	}, source.Name)
	if safeName == "" {
		safeName = "query"
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.QueryEscape(safeName+".csv"))
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
	summary, err := s.database.StreamQuery(r.Context(), source, req.SQL, req.MaxRows, emit)
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
		s.audit.Write("database.export", "source_id", source.ID, "kind", source.Kind, "query_id", queryID, "result", "fail", "error", trim(err.Error(), 300))
		return
	}
	s.audit.Write("database.export", "source_id", source.ID, "kind", source.Kind, "query_id", queryID,
		"rows", summary.Rows, "elapsed_ms", summary.ElapsedMS, "truncated", summary.Truncated, "result", "ok")
}

func databaseCSVValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	if raw, err := json.Marshal(value); err == nil {
		return string(raw)
	}
	return fmt.Sprint(value)
}

func safeCSVCell(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
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
	cursor, _ := strconv.ParseUint(r.URL.Query().Get("cursor"), 10, 64)
	count, _ := strconv.ParseInt(r.URL.Query().Get("count"), 10, 64)
	result, err := s.database.RedisScan(r.Context(), source, cursor, r.URL.Query().Get("pattern"), count)
	if err != nil {
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
	dec := json.NewDecoder(io.LimitReader(r.Body, databaseBodyLimit))
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
	return errors.New(message)
}
