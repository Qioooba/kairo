package httpserver

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"kairo/internal/dbconsole"
)

type databaseScriptRequest struct {
	SourceID   string                    `json:"source_id"`
	SessionID  string                    `json:"session_id,omitempty"`
	SQL        string                    `json:"sql"`
	Parameters []dbconsole.BindParameter `json:"parameters,omitempty"`
	Options    dbconsole.ScriptOptions   `json:"options,omitempty"`
	Confirm    bool                      `json:"confirm,omitempty"`
}

func (s *Server) handleDatabaseTransactionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	if !validDatabaseSessionID(sessionID) {
		writeErr(w, http.StatusBadRequest, errors.New("session_id 无效"))
		return
	}
	state := s.database.GetTransactionStatus(source, sessionID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "transaction": state})
}

func (s *Server) handleDatabaseTransactionList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	if _, ok := s.databaseSourceFromQuery(w, r); !ok {
		return
	}
	sourceID := strings.TrimSpace(r.URL.Query().Get("source_id"))
	items := s.database.ListTransactionStatus(sourceID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "transactions": items})
}

func (s *Server) handleDatabaseScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req databaseScriptRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.SessionID != "" && !validDatabaseSessionID(req.SessionID) {
		writeErr(w, http.StatusBadRequest, errors.New("session_id 无效"))
		return
	}
	if strings.TrimSpace(req.SQL) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("sql 不能为空"))
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	if source.ReadOnly {
		writeErr(w, http.StatusForbidden, errors.New("该数据源处于只读锁定状态"))
		return
	}
	if source.IsProduction() && !req.Confirm {
		writeErr(w, http.StatusBadRequest, errors.New("生产数据源脚本执行需要 confirm=true"))
		return
	}
	result, err := s.database.ExecuteScript(r.Context(), source, req.SQL, req.SessionID, req.Parameters, req.Options)
	if err != nil {
		s.audit.Write("database.script", "source_id", source.ID, "result", "fail", "error", trim(err.Error(), 300))
		// The per-statement errors are useful to the UI, while the outer error is
		// kept generic to avoid exposing DSN/password material.
		if len(result.Statements) > 0 {
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "result": result, "error": trim(s.databaseSafeError(source, err).Error(), 1000)})
		} else {
			writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		}
		return
	}
	s.audit.Write("database.script", "source_id", source.ID, "statements", len(result.Statements), "rows_affected", result.RowsAffected, "result", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}

type databaseGridRequest struct {
	SourceID  string                   `json:"source_id"`
	Schema    string                   `json:"schema"`
	Table     string                   `json:"table"`
	Mutations []dbconsole.GridMutation `json:"mutations"`
	SessionID string                   `json:"session_id"`
	Commit    bool                     `json:"commit,omitempty"`
	Confirm   bool                     `json:"confirm,omitempty"`
}

func (s *Server) handleDatabaseGrid(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req databaseGridRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !validDatabaseSessionID(req.SessionID) {
		writeErr(w, http.StatusBadRequest, errors.New("session_id 无效"))
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	result, err := s.database.ApplyGridMutations(r.Context(), source, dbconsole.GridMutationRequest{
		Schema: req.Schema, Table: req.Table, Mutations: req.Mutations, SessionID: req.SessionID, Commit: req.Commit, Confirm: req.Confirm,
	})
	if err != nil {
		s.audit.Write("database.grid", "source_id", source.ID, "result", "fail", "error", trim(err.Error(), 300))
		if len(result.Results) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "result": result, "error": trim(s.databaseSafeError(source, err).Error(), 1000)})
		} else {
			writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		}
		return
	}
	s.audit.Write("database.grid", "source_id", source.ID, "rows_affected", result.RowsAffected, "result", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}

type databaseImportPreviewRequest struct {
	SourceID   string `json:"source_id"`
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	Format     string `json:"format"`
	DataBase64 string `json:"data_base64"`
}

func decodeImportData(value string) ([]byte, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("data_base64 不能为空")
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("data_base64 无效: %w", err)
	}
	return data, nil
}

func (s *Server) handleDatabaseImportPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req databaseImportPreviewRequest
	if err := decodeDatabaseJSONLimit(r, &req, 12<<20); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	data, err := decodeImportData(req.DataBase64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	preview, err := s.database.PreviewImport(r.Context(), source, req.Schema, req.Table, req.Format, data)
	if err != nil {
		writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		return
	}
	s.audit.Write("database.import.preview", "source_id", source.ID, "schema", req.Schema, "table", req.Table, "rows", preview.PreviewRows, "result", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preview": preview})
}

type databaseImportApplyRequest struct {
	SourceID   string                    `json:"source_id"`
	Schema     string                    `json:"schema"`
	Table      string                    `json:"table"`
	Mappings   []dbconsole.ImportMapping `json:"mappings"`
	Rows       [][]string                `json:"rows"`
	Format     string                    `json:"format,omitempty"`
	DataBase64 string                    `json:"data_base64,omitempty"`
	SessionID  string                    `json:"session_id"`
	Commit     bool                      `json:"commit,omitempty"`
	Confirm    bool                      `json:"confirm,omitempty"`
}

// Reparse the original upload for execution. Preview rows are deliberately
// bounded and must never become the source of truth for an import.
func (req databaseImportApplyRequest) importRows() ([][]string, error) {
	if req.DataBase64 == "" {
		if req.Format != "" {
			return nil, errors.New("文件导入缺少 data_base64")
		}
		return req.Rows, nil
	}
	if len(req.Rows) != 0 {
		return nil, errors.New("rows 与 data_base64 不能同时指定")
	}
	data, err := decodeImportData(req.DataBase64)
	if err != nil {
		return nil, err
	}
	table, err := dbconsole.ParseImportData(req.Format, data)
	if err != nil {
		return nil, err
	}
	return table.Rows, nil
}

func (s *Server) handleDatabaseImportApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req databaseImportApplyRequest
	if err := decodeDatabaseJSONLimit(r, &req, 12<<20); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	rows, err := req.importRows()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.database.ApplyImport(r.Context(), source, dbconsole.ImportApplyRequest{
		Schema: req.Schema, Table: req.Table, Mappings: req.Mappings, Rows: rows, SessionID: req.SessionID, Commit: req.Commit, Confirm: req.Confirm,
	})
	if err != nil {
		s.audit.Write("database.import", "source_id", source.ID, "result", "fail", "error", trim(err.Error(), 300))
		if len(result.Rows) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "result": result, "error": trim(s.databaseSafeError(source, err).Error(), 1000)})
		} else {
			writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		}
		return
	}
	s.audit.Write("database.import", "source_id", source.ID, "rows", result.Processed, "result", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}
