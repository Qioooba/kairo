package httpserver

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"kairo/internal/dbconsole"
)

// objectStudioRequest embeds the typed change so clients can send a flat JSON
// object ({source_id, action, object_type, ...}), which keeps the endpoint
// convenient for the existing database page.
type objectStudioRequest struct {
	SourceID      string `json:"source_id"`
	SourceIDCamel string `json:"sourceId,omitempty"`
	dbconsole.ObjectStudioChange
}

type oracleFunctionRequest struct {
	SourceID string `json:"source_id"`
	dbconsole.OracleFunctionChange
}

type legacyFunctionCompileRequest struct {
	SourceID      string `json:"sourceId"`
	SourceIDSnake string `json:"source_id,omitempty"`
	Schema        string `json:"schema"`
	Object        string `json:"object"`
	Type          string `json:"type"`
	Source        string `json:"source"`
	Dialect       string `json:"dialect,omitempty"`
	Confirm       bool   `json:"confirm,omitempty"`
}

// legacyDDLRequest is accepted by the first object-designer UI.  DDL itself
// is retained only as a hint for create vs alter; it is never executed.  The
// actual operation is rebuilt from Fields/Indexes/Constraints and therefore
// receives the same identifier and value validation as the typed endpoint.
type legacyDDLRequest struct {
	SourceID      string                `json:"sourceId"`
	SourceIDSnake string                `json:"source_id,omitempty"`
	Schema        string                `json:"schema"`
	Object        string                `json:"object"`
	Type          string                `json:"type"`
	DDL           string                `json:"ddl"`
	Fields        []legacyDDLField      `json:"fields,omitempty"`
	Indexes       []legacyDDLIndex      `json:"indexes,omitempty"`
	Constraints   []legacyDDLConstraint `json:"constraints,omitempty"`
	Confirm       bool                  `json:"confirm,omitempty"`
}

type legacyDDLField struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	DataType     string `json:"data_type,omitempty"`
	Nullable     bool   `json:"nullable"`
	DefaultValue string `json:"defaultValue,omitempty"`
	Default      string `json:"default,omitempty"`
	Primary      bool   `json:"primary,omitempty"`
	PrimaryKey   bool   `json:"primary_key,omitempty"`
	Comment      string `json:"comment,omitempty"`
}

type legacyDDLIndex struct {
	Name    string `json:"name"`
	Columns string `json:"columns"`
	Unique  bool   `json:"unique,omitempty"`
}

type legacyDDLConstraint struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

func (s *Server) handleDatabaseObjectStudio(w http.ResponseWriter, r *http.Request, suffix string) {
	suffix = strings.Trim(strings.TrimSpace(suffix), "/")
	switch suffix {
	case "capabilities":
		s.handleDatabaseObjectCapabilities(w, r)
	case "structure":
		s.handleDatabaseObjectStructure(w, r)
	case "dependencies":
		s.handleDatabaseObjectDependencies(w, r)
	case "invalid":
		s.handleDatabaseInvalidObjects(w, r)
	case "preview":
		s.handleDatabaseObjectPreview(w, r)
	case "apply":
		s.handleDatabaseObjectApply(w, r)
	case "function", "function/source":
		s.handleDatabaseFunctionSource(w, r)
	case "function/status":
		s.handleDatabaseFunctionStatus(w, r)
	case "function/preview":
		s.handleDatabaseFunctionPreview(w, r)
	case "function/compile", "function/apply":
		s.handleDatabaseFunctionCompile(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleDatabaseObjectCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return
	}
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "kind": source.Kind, "capabilities": dbconsole.ObjectStudioCapabilities(source.Kind),
	})
}

func (s *Server) handleDatabaseObjectStructure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return
	}
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	item, err := s.database.ObjectStudioStructure(r.Context(), source, query.Get("schema"), query.Get("object"), query.Get("type"))
	if err != nil {
		writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "structure": item})
}

func (s *Server) handleDatabaseObjectDependencies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return
	}
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	items, err := s.database.ObjectDependencies(r.Context(), source, query.Get("schema"), query.Get("object"), query.Get("type"))
	if err != nil {
		writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dependencies": items})
}

func (s *Server) handleDatabaseInvalidObjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return
	}
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	items, err := s.database.InvalidObjects(r.Context(), source, query.Get("schema"), query.Get("search"))
	if err != nil {
		writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "objects": items, "supported": source.Kind == dbconsole.KindOracle})
}

func (s *Server) decodeObjectStudioRequest(w http.ResponseWriter, r *http.Request) (dbconsole.Source, dbconsole.ObjectStudioChange, bool) {
	var req objectStudioRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return dbconsole.Source{}, dbconsole.ObjectStudioChange{}, false
	}
	if req.SourceID == "" {
		req.SourceID = req.SourceIDCamel
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return dbconsole.Source{}, dbconsole.ObjectStudioChange{}, false
	}
	return source, req.ObjectStudioChange, true
}

func (s *Server) handleDatabaseObjectPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	source, change, ok := s.decodeObjectStudioRequest(w, r)
	if !ok {
		return
	}
	plan, err := s.database.PreviewObjectStudio(source, change)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plan": plan})
}

func (s *Server) handleDatabaseObjectApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	source, change, ok := s.decodeObjectStudioRequest(w, r)
	if !ok {
		return
	}
	result, err := s.database.ApplyObjectStudio(r.Context(), source, change)
	if err != nil {
		writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		return
	}
	s.audit.Write("database.object_studio.apply", "source_id", source.ID, "kind", source.Kind, "object_type", change.ObjectType, "action", change.Action, "applied", result.Applied, "result", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}

func (s *Server) functionQuerySource(w http.ResponseWriter, r *http.Request) (dbconsole.Source, string, string, bool) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return dbconsole.Source{}, "", "", false
	}
	query := r.URL.Query()
	source, ok := s.databaseSourceFromQuery(w, r)
	if !ok {
		return dbconsole.Source{}, "", "", false
	}
	name := query.Get("name")
	if name == "" {
		name = query.Get("object")
	}
	return source, query.Get("schema"), name, true
}

func (s *Server) handleDatabaseFunctionSource(w http.ResponseWriter, r *http.Request) {
	source, schema, name, ok := s.functionQuerySource(w, r)
	if !ok {
		return
	}
	item, err := s.database.LoadOracleFunction(r.Context(), source, schema, name)
	if err != nil {
		writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "source": item.Source, "function": item})
}

func (s *Server) handleDatabaseFunctionStatus(w http.ResponseWriter, r *http.Request) {
	source, schema, name, ok := s.functionQuerySource(w, r)
	if !ok {
		return
	}
	status, known, err := s.database.ObjectStatus(r.Context(), source, schema, name, "FUNCTION")
	if err != nil {
		writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "schema": schema, "name": name, "status": status, "valid": known && strings.EqualFold(status, "VALID"), "known": known})
}

func (s *Server) decodeFunctionRequest(w http.ResponseWriter, r *http.Request) (dbconsole.Source, dbconsole.OracleFunctionChange, bool) {
	var req oracleFunctionRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return dbconsole.Source{}, dbconsole.OracleFunctionChange{}, false
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return dbconsole.Source{}, dbconsole.OracleFunctionChange{}, false
	}
	return source, req.OracleFunctionChange, true
}

func (s *Server) handleDatabaseFunctionPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	source, change, ok := s.decodeFunctionRequest(w, r)
	if !ok {
		return
	}
	if source.Kind != dbconsole.KindOracle {
		writeErr(w, http.StatusBadRequest, errors.New("只有 Oracle 支持 Function 源码编辑"))
		return
	}
	// Compilation validation is deliberately shared with the execution path;
	// use a zero manager call only for source normalization by returning a
	// lightweight preview response from the pure plan helper below.
	sqlText, err := dbconsole.NormalizeOracleFunctionSource(change)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sql": sqlText, "kind": source.Kind})
}

func (s *Server) handleDatabaseFunctionCompile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	source, change, ok := s.decodeFunctionRequest(w, r)
	if !ok {
		return
	}
	if !change.Confirm {
		writeErr(w, http.StatusBadRequest, errors.New("替换 Function 需要 confirm=true"))
		return
	}
	result, err := s.database.CompileOracleFunction(r.Context(), source, change)
	if err != nil {
		writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		return
	}
	s.audit.Write("database.function.compile", "source_id", source.ID, "schema", change.Schema, "name", change.Name, "success", result.Success, "errors", len(result.Errors))
	// Compiler diagnostics are a successful API request even when compilation
	// itself failed; the editor needs the line/column list in the response.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}

// handleDatabaseFunctionCompileLegacy keeps the first object-designer module
// usable while clients migrate to /object-studio/function/compile.  It does
// not accept arbitrary SQL: the payload is translated into the same typed
// OracleFunctionChange and the exact same source validation is applied.
func (s *Server) handleDatabaseFunctionCompileLegacy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req legacyFunctionCompileRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.SourceID == "" {
		req.SourceID = req.SourceIDSnake
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	if source.Kind != dbconsole.KindOracle {
		writeErr(w, http.StatusBadRequest, errors.New("只有 Oracle 支持 Function 源码编译"))
		return
	}
	if source.IsProduction() && !req.Confirm {
		writeErr(w, http.StatusBadRequest, errors.New("生产数据源编译 Function 需要 confirm=true"))
		return
	}
	change := dbconsole.OracleFunctionChange{Schema: req.Schema, Name: req.Object, Source: req.Source, Replace: true, Confirm: true}
	result, err := s.database.CompileOracleFunction(r.Context(), source, change)
	if err != nil {
		writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, err))
		return
	}
	s.audit.Write("database.function.compile", "source_id", source.ID, "schema", change.Schema, "name", change.Name, "success", result.Success, "errors", len(result.Errors), "legacy", true)
	// Expose diagnostics both at the top level (legacy editor contract) and in
	// the typed result (new contract).
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "success": result.Success, "errors": result.Errors, "result": result})
}

func legacyDDLAction(ddl string, object string) (string, error) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(ddl), ";"))
	if trimmed == "" {
		return "", errors.New("DDL 不能为空")
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "CREATE TABLE ") || strings.HasPrefix(upper, "CREATE TABLE\n") || strings.HasPrefix(upper, "CREATE TABLE\r") {
		return "create", nil
	}
	if strings.HasPrefix(upper, "ALTER TABLE ") || strings.HasPrefix(upper, "ALTER TABLE\n") || strings.HasPrefix(upper, "ALTER TABLE\r") {
		return "alter", nil
	}
	return "", fmt.Errorf("对象 %s 的 DDL 仅支持 CREATE TABLE 或 ALTER TABLE", object)
}

func legacyFieldsToColumns(fields []legacyDDLField) ([]dbconsole.ObjectStudioColumn, []string) {
	columns := make([]dbconsole.ObjectStudioColumn, 0, len(fields))
	primary := make([]string, 0)
	for _, field := range fields {
		typ := strings.TrimSpace(field.Type)
		if typ == "" {
			typ = strings.TrimSpace(field.DataType)
		}
		def := strings.TrimSpace(field.Default)
		if def == "" {
			def = strings.TrimSpace(field.DefaultValue)
		}
		nullable := field.Nullable
		columns = append(columns, dbconsole.ObjectStudioColumn{
			Name: field.Name, DataType: typ, Nullable: &nullable, Default: def,
			Comment: field.Comment, PrimaryKey: field.Primary || field.PrimaryKey,
		})
		if field.Primary || field.PrimaryKey {
			primary = append(primary, field.Name)
		}
	}
	return columns, primary
}

func legacyIndexColumns(value string) []string {
	parts := strings.Split(value, ",")
	columns := make([]string, 0, len(parts))
	for _, part := range parts {
		if name := strings.TrimSpace(part); name != "" {
			columns = append(columns, name)
		}
	}
	return columns
}

func parseLegacyConstraint(name, table, schema, definition string) (dbconsole.ObjectStudioConstraint, error) {
	definition = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(definition), ";"))
	upper := strings.ToUpper(definition)
	constraint := dbconsole.ObjectStudioConstraint{Name: name, Table: table}
	switch {
	case strings.HasPrefix(upper, "PRIMARY KEY"):
		constraint.Type = "primary"
		constraint.Columns = parseLegacyParenColumns(definition[len("PRIMARY KEY"):])
	case strings.HasPrefix(upper, "UNIQUE"):
		constraint.Type = "unique"
		constraint.Columns = parseLegacyParenColumns(definition[len("UNIQUE"):])
	case strings.HasPrefix(upper, "CHECK"):
		constraint.Type = "check"
		open := strings.Index(definition, "(")
		if open < 0 || !strings.HasSuffix(definition, ")") {
			return dbconsole.ObjectStudioConstraint{}, errors.New("CHECK 约束格式非法")
		}
		constraint.Expression = strings.TrimSpace(definition[open+1 : len(definition)-1])
	case strings.HasPrefix(upper, "FOREIGN KEY"):
		constraint.Type = "foreign"
		close := strings.Index(definition, ")")
		if close < 0 {
			return dbconsole.ObjectStudioConstraint{}, errors.New("FOREIGN KEY 本地列格式非法")
		}
		constraint.Columns = parseLegacyParenColumns(definition[len("FOREIGN KEY") : close+1])
		rest := strings.TrimSpace(definition[close+1:])
		refRe := regexp.MustCompile(`(?is)^REFERENCES\s+([A-Za-z_][A-Za-z0-9_$#]*)(?:\.([A-Za-z_][A-Za-z0-9_$#]*))?\s*\(([^)]*)\)(?:\s+ON\s+DELETE\s+(.+))?$`)
		match := refRe.FindStringSubmatch(rest)
		if len(match) != 5 {
			return dbconsole.ObjectStudioConstraint{}, errors.New("FOREIGN KEY REFERENCES 格式非法")
		}
		constraint.ReferencedSchema, constraint.ReferencedTable = schema, match[1]
		if match[2] != "" {
			constraint.ReferencedSchema, constraint.ReferencedTable = match[1], match[2]
		}
		constraint.ReferencedColumns = parseLegacyParenColumns("(" + match[3] + ")")
		constraint.OnDelete = strings.TrimSpace(match[4])
	default:
		return dbconsole.ObjectStudioConstraint{}, errors.New("约束定义仅支持 PRIMARY KEY/UNIQUE/FOREIGN KEY/CHECK")
	}
	if len(constraint.Columns) == 0 && constraint.Type != "check" {
		return dbconsole.ObjectStudioConstraint{}, errors.New("约束必须包含列")
	}
	return constraint, nil
}

func parseLegacyParenColumns(value string) []string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "(") || !strings.HasSuffix(value, ")") {
		return nil
	}
	parts := strings.Split(value[1:len(value)-1], ",")
	columns := make([]string, 0, len(parts))
	for _, part := range parts {
		if name := strings.TrimSpace(part); name != "" {
			columns = append(columns, name)
		}
	}
	return columns
}

// handleDatabaseDDLLegacy translates the original designer payload to typed
// operations.  Raw DDL is never executed.  This endpoint is kept only as a
// compatibility bridge; new clients should use /object-studio/preview and
// /object-studio/apply directly.
func (s *Server) handleDatabaseDDLLegacy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var req legacyDDLRequest
	if err := decodeDatabaseJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.SourceID == "" {
		req.SourceID = req.SourceIDSnake
	}
	source, ok := s.databaseSourceForRequest(w, r, req.SourceID)
	if !ok {
		return
	}
	if source.Kind != dbconsole.KindOracle && source.Kind != dbconsole.KindMySQL {
		writeErr(w, http.StatusBadRequest, errors.New("Redis 不支持对象 DDL"))
		return
	}
	if source.IsProduction() && !req.Confirm {
		writeErr(w, http.StatusBadRequest, errors.New("生产数据源对象变更需要 confirm=true"))
		return
	}
	if !source.DDLAllowed() {
		writeErr(w, http.StatusBadRequest, errors.New("当前数据源未开启 DDL 能力（allow_ddl=true），或处于只读模式"))
		return
	}
	action, err := legacyDDLAction(req.DDL, req.Object)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	typ := strings.ToLower(strings.TrimSpace(req.Type))
	if typ == "" {
		typ = "TABLE"
	}
	if !strings.EqualFold(typ, "TABLE") {
		writeErr(w, http.StatusBadRequest, errors.New("兼容 DDL 端点仅支持 TABLE；其他对象请使用 object-studio typed API"))
		return
	}
	columns, primary := legacyFieldsToColumns(req.Fields)
	changes := []dbconsole.ObjectStudioChange{{Action: action, ObjectType: "table", Schema: req.Schema, Name: req.Object, Columns: columns, PrimaryKey: primary, Confirm: true}}
	if action == "alter" {
		// The old UI sends the complete current field list.  Only add columns
		// that are absent on the server, avoiding duplicate ADD operations.
		if current, fieldsErr := s.database.Fields(r.Context(), source, req.Schema, req.Object); fieldsErr == nil {
			seen := make(map[string]bool, len(current))
			for _, field := range current {
				seen[strings.ToUpper(field.Name)] = true
			}
			filtered := make([]dbconsole.ObjectStudioColumn, 0, len(columns))
			for _, column := range columns {
				if !seen[strings.ToUpper(column.Name)] {
					filtered = append(filtered, column)
				}
			}
			columns = filtered
			changes[0].Columns = columns
			changes[0].Changes = make([]dbconsole.ObjectStudioColumnChange, 0, len(columns))
			for _, column := range columns {
				changes[0].Changes = append(changes[0].Changes, dbconsole.ObjectStudioColumnChange{Action: "add_column", Column: column})
			}
		}
		if len(changes[0].Changes) == 0 {
			changes = changes[:0]
		}
	}
	for _, index := range req.Indexes {
		if strings.TrimSpace(index.Name) == "" {
			continue
		}
		changes = append(changes, dbconsole.ObjectStudioChange{Action: "create", ObjectType: "index", Schema: req.Schema, Name: index.Name, Table: req.Object, Confirm: true, Index: &dbconsole.ObjectStudioIndex{Name: index.Name, Table: req.Object, Columns: legacyIndexColumns(index.Columns), Unique: index.Unique}})
	}
	for _, raw := range req.Constraints {
		if strings.TrimSpace(raw.Name) == "" && strings.TrimSpace(raw.Definition) == "" {
			continue
		}
		constraint, parseErr := parseLegacyConstraint(raw.Name, req.Object, req.Schema, raw.Definition)
		if parseErr != nil {
			writeErr(w, http.StatusBadRequest, parseErr)
			return
		}
		changes = append(changes, dbconsole.ObjectStudioChange{Action: "create", ObjectType: "constraint", Schema: req.Schema, Name: raw.Name, Table: req.Object, Confirm: true, Constraint: &constraint})
	}
	if len(changes) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("没有可执行的结构变更"))
		return
	}
	plans := make([]dbconsole.ObjectStudioPlan, 0, len(changes))
	for _, change := range changes {
		plan, planErr := dbconsole.BuildObjectStudioPlan(source, change)
		if planErr != nil {
			writeErr(w, http.StatusBadRequest, planErr)
			return
		}
		plans = append(plans, plan)
	}
	results := make([]dbconsole.ObjectStudioApplyResult, 0, len(changes))
	for _, change := range changes {
		result, applyErr := s.database.ApplyObjectStudio(r.Context(), source, change)
		if applyErr != nil {
			writeErrSanitized(w, http.StatusBadGateway, s.databaseSafeError(source, applyErr))
			return
		}
		results = append(results, result)
	}
	s.audit.Write("database.object_studio.apply", "source_id", source.ID, "schema", req.Schema, "object", req.Object, "legacy", true, "result", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plans": plans, "results": results})
}
