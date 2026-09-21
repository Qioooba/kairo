package dbconsole

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const maxGridMutations = 200

// GridMutation is the typed request used by the result-grid editor.  Values
// are new values; Original is the snapshot read by the grid and is used for
// optimistic concurrency checks.  Key must contain every PK column unless a
// native Oracle ROWID is supplied.
type GridMutation struct {
	Action     string         `json:"action"` // insert/update/delete
	Values     map[string]any `json:"values,omitempty"`
	Original   map[string]any `json:"original,omitempty"`
	Key        map[string]any `json:"key,omitempty"`
	PrimaryKey []string       `json:"primary_key,omitempty"`
	RowID      string         `json:"rowid,omitempty"`
	UseRowID   bool           `json:"use_rowid,omitempty"`
	Confirm    bool           `json:"confirm,omitempty"`
}

type GridMutationRequest struct {
	Schema    string         `json:"schema"`
	Table     string         `json:"table"`
	Mutations []GridMutation `json:"mutations"`
	SessionID string         `json:"session_id,omitempty"`
	Commit    bool           `json:"commit,omitempty"`
	Confirm   bool           `json:"confirm,omitempty"`
}

type GridMutationResult struct {
	Index        int    `json:"index"`
	Action       string `json:"action"`
	Status       string `json:"status"` // succeeded/conflict/failed/skipped
	RowsAffected int64  `json:"rows_affected"`
	Conflict     bool   `json:"conflict,omitempty"`
	Error        string `json:"error,omitempty"`
}

type GridMutationSummary struct {
	Results            []GridMutationResult `json:"results"`
	RowsAffected       int64                `json:"rows_affected"`
	ElapsedMS          int64                `json:"elapsed_ms"`
	Committed          bool                 `json:"committed"`
	RolledBack         bool                 `json:"rolled_back"`
	TransactionPending bool                 `json:"transaction_pending"`
}

func quoteGridIdentifier(kind, name, label string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "\x00\r\n;\"`") {
		return "", fmt.Errorf("%s 非法", label)
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '$' || r == '#' {
			if i == 0 && r >= '0' && r <= '9' {
				return "", fmt.Errorf("%s 必须以字母或下划线开头", label)
			}
			if kind == KindMySQL && (r == '$' || r == '#') {
				return "", fmt.Errorf("%s 包含 MySQL 不支持的字符", label)
			}
			continue
		}
		return "", fmt.Errorf("%s 包含非法字符", label)
	}
	if kind == KindOracle {
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`", nil
}

func gridQualifiedTable(kind, schema, table string) (string, error) {
	tableSQL, err := quoteGridIdentifier(kind, table, "表名")
	if err != nil {
		return "", err
	}
	schema = strings.TrimSpace(schema)
	if schema == "" || schema == "加载中…" || schema == "加载失败" {
		return tableSQL, nil
	}
	schemaSQL, err := quoteGridIdentifier(kind, schema, "schema")
	if err != nil {
		return "", err
	}
	return schemaSQL + "." + tableSQL, nil
}

func gridMarker(kind string, index int) string {
	if kind == KindOracle {
		return fmt.Sprintf(":kairo_p%d", index)
	}
	return "?"
}

func sortedGridKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.ToUpper(keys[i]) < strings.ToUpper(keys[j]) })
	return keys
}

func gridValueEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// BuildGridMutationSQL builds a parameterized single-row mutation.  It does
// not connect or execute anything, making it safe for preview and easy to
// unit-test.  Rows affected are validated by ApplyGridMutations.
func BuildGridMutationSQL(kind, schema, table string, mutation GridMutation) (string, []any, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != KindOracle && kind != KindMySQL {
		return "", nil, errors.New("网格编辑仅支持 Oracle/MySQL")
	}
	for _, values := range []map[string]any{mutation.Values, mutation.Key, mutation.Original} {
		for name, value := range values {
			if _, err := driver.DefaultParameterConverter.ConvertValue(value); err != nil {
				return "", nil, fmt.Errorf("列 %s 包含复杂或截断值，请排除 LOB 预览后重新编辑", name)
			}
		}
	}
	tableSQL, err := gridQualifiedTable(kind, schema, table)
	if err != nil {
		return "", nil, err
	}
	action := strings.ToLower(strings.TrimSpace(mutation.Action))
	if action != "insert" && action != "update" && action != "delete" {
		return "", nil, errors.New("网格操作仅支持 insert/update/delete")
	}
	args := make([]any, 0)
	var sqlText strings.Builder
	paramIndex := 0
	// markerWithValue avoids exposing a value in generated SQL.
	markerWithValue := func(value any) string {
		if kind == KindOracle {
			paramIndex++
			args = append(args, sql.Named(fmt.Sprintf("kairo_p%d", paramIndex), value))
			return fmt.Sprintf(":kairo_p%d", paramIndex)
		}
		paramIndex++
		args = append(args, value)
		return "?"
	}
	if action == "insert" {
		keys := sortedGridKeys(mutation.Values)
		if len(keys) == 0 {
			return "", nil, errors.New("insert 至少需要一列")
		}
		cols := make([]string, 0, len(keys))
		vals := make([]string, 0, len(keys))
		for _, key := range keys {
			col, qErr := quoteGridIdentifier(kind, key, "列名")
			if qErr != nil {
				return "", nil, qErr
			}
			cols = append(cols, col)
			vals = append(vals, markerWithValue(mutation.Values[key]))
		}
		fmt.Fprintf(&sqlText, "INSERT INTO %s (%s) VALUES (%s)", tableSQL, strings.Join(cols, ", "), strings.Join(vals, ", "))
		return sqlText.String(), args, nil
	}
	if action == "delete" {
		whereSQL, whereArgs, whereErr := buildGridWhere(kind, mutation, 0)
		if whereErr != nil {
			return "", nil, whereErr
		}
		args = append(args, whereArgs...)
		return fmt.Sprintf("DELETE FROM %s WHERE %s", tableSQL, whereSQL), args, nil
	}
	keys := sortedGridKeys(mutation.Values)
	if len(keys) == 0 {
		return "", nil, errors.New("update 至少需要一列")
	}
	sets := make([]string, 0, len(keys))
	// UPDATE's SET markers must precede WHERE markers in the args slice.  The
	// helper above generated where args first, so rebuild deterministically.
	args = args[:0]
	paramIndex = 0
	for _, key := range keys {
		col, qErr := quoteGridIdentifier(kind, key, "列名")
		if qErr != nil {
			return "", nil, qErr
		}
		sets = append(sets, col+" = "+markerWithValue(mutation.Values[key]))
	}
	whereSQL, whereArgs, whereErr := buildGridWhere(kind, mutation, len(keys))
	if whereErr != nil {
		return "", nil, whereErr
	}
	args = append(args, whereArgs...)
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s", tableSQL, strings.Join(sets, ", "), whereSQL), args, nil
}

func buildGridWhere(kind string, mutation GridMutation, offset int) (string, []any, error) {
	parts := make([]string, 0, len(mutation.PrimaryKey)+len(mutation.Original)+1)
	args := make([]any, 0, cap(parts))
	if mutation.UseRowID {
		if kind != KindOracle {
			return "", nil, errors.New("MySQL 不支持 ROWID")
		}
		if strings.TrimSpace(mutation.RowID) == "" || strings.ContainsAny(mutation.RowID, "\x00\r\n'\"") {
			return "", nil, errors.New("ROWID 无效")
		}
		parts = append(parts, "ROWID = "+gridMarker(kind, offset+1))
		args = append(args, sql.Named(fmt.Sprintf("kairo_p%d", offset+1), mutation.RowID))
	}
	if !mutation.UseRowID && len(mutation.PrimaryKey) == 0 {
		return "", nil, errors.New("update/delete 必须提供主键列或 Oracle ROWID")
	}
	key := mutation.Key
	if len(key) == 0 {
		key = mutation.Original
	}
	primaryKey := mutation.PrimaryKey
	if mutation.UseRowID {
		primaryKey = nil
	}
	for _, name := range primaryKey {
		value, ok := key[name]
		if !ok {
			// tolerate case-only JSON key differences while retaining the
			// canonical column name in generated SQL.
			for candidate, candidateValue := range key {
				if strings.EqualFold(candidate, name) {
					value, ok = candidateValue, true
					break
				}
			}
		}
		if !ok {
			return "", nil, fmt.Errorf("缺少主键值 %s", name)
		}
		col, err := quoteGridIdentifier(kind, name, "主键列")
		if err != nil {
			return "", nil, err
		}
		if value == nil {
			parts = append(parts, col+" IS NULL")
			continue
		}
		markerIndex := offset + len(args) + 1
		parts = append(parts, col+" = "+gridMarker(kind, markerIndex))
		if kind == KindOracle {
			args = append(args, sql.Named(fmt.Sprintf("kairo_p%d", markerIndex), value))
		} else {
			args = append(args, value)
		}
	}
	if len(parts) == 0 {
		return "", nil, errors.New("主键条件不能为空")
	}
	// Include changed-row snapshot columns for optimistic locking.  Only
	// columns in Original are used; absent snapshots preserve key-only updates.
	for _, name := range sortedGridKeys(mutation.Original) {
		found := false
		for _, pk := range primaryKey {
			if strings.EqualFold(pk, name) {
				found = true
				break
			}
		}
		if found {
			continue
		}
		value := mutation.Original[name]
		col, err := quoteGridIdentifier(kind, name, "原值列")
		if err != nil {
			return "", nil, err
		}
		if value == nil {
			parts = append(parts, col+" IS NULL")
		} else {
			markerIndex := offset + len(args) + 1
			parts = append(parts, col+" = "+gridMarker(kind, markerIndex))
			if kind == KindOracle {
				args = append(args, sql.Named(fmt.Sprintf("kairo_p%d", markerIndex), value))
			} else {
				args = append(args, value)
			}
		}
	}
	return strings.Join(parts, " AND "), args, nil
}

// ApplyGridMutations executes bounded mutations inside the page-tab
// transaction.  Every UPDATE/DELETE must affect exactly one row; zero means
// an optimistic-lock conflict and more than one is treated as unsafe.
func (m *Manager) ApplyGridMutations(ctx context.Context, source Source, req GridMutationRequest) (GridMutationSummary, error) {
	if !source.MutationAllowed() {
		return GridMutationSummary{}, errors.New("该数据源处于只读锁定状态")
	}
	if source.IsProduction() && !req.Confirm {
		return GridMutationSummary{}, errors.New("生产数据源网格写入需要 confirm=true")
	}
	if len(req.Mutations) == 0 || len(req.Mutations) > maxGridMutations {
		return GridMutationSummary{}, fmt.Errorf("mutations 数量必须在 1..%d 之间", maxGridMutations)
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return GridMutationSummary{}, errors.New("网格写入必须绑定 session_id")
	}
	if !validGridSessionID(req.SessionID) {
		return GridMutationSummary{}, errors.New("session_id 无效")
	}
	req.Schema = ResolveSchema(source, req.Schema)
	queryCtx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(queryCtx); err != nil {
		return GridMutationSummary{}, err
	}
	defer m.release()
	entry, err := m.transactionForContext(queryCtx, source, req.SessionID, true)
	if err != nil {
		return GridMutationSummary{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	started := time.Now()
	summary := GridMutationSummary{Results: make([]GridMutationResult, len(req.Mutations))}
	for i, mutation := range req.Mutations {
		summary.Results[i] = GridMutationResult{Index: i, Action: strings.ToLower(strings.TrimSpace(mutation.Action)), RowsAffected: -1, Status: "failed"}
		sqlText, args, buildErr := BuildGridMutationSQL(source.Kind, req.Schema, req.Table, mutation)
		if buildErr != nil {
			summary.Results[i].Error = buildErr.Error()
			m.rollbackEntryLocked(source.ID, req.SessionID, entry)
			summary.RolledBack = true
			return summary, buildErr
		}
		res, execErr := entry.tx.ExecContext(queryCtx, sqlText, args...)
		if execErr != nil {
			summary.Results[i].Error = execErr.Error()
			m.rollbackEntryLocked(source.ID, req.SessionID, entry)
			summary.RolledBack = true
			return summary, execErr
		}
		affected, affectedErr := res.RowsAffected()
		if affectedErr != nil {
			summary.Results[i].Error = affectedErr.Error()
			m.rollbackEntryLocked(source.ID, req.SessionID, entry)
			summary.RolledBack = true
			return summary, affectedErr
		}
		summary.Results[i].RowsAffected = affected
		if affected != 1 {
			summary.Results[i].Status = "conflict"
			summary.Results[i].Conflict = true
			summary.Results[i].Error = fmt.Sprintf("并发冲突：期望影响 1 行，实际影响 %d 行", affected)
			m.rollbackEntryLocked(source.ID, req.SessionID, entry)
			summary.RolledBack = true
			return summary, errors.New(summary.Results[i].Error)
		}
		summary.Results[i].Status = "succeeded"
		summary.RowsAffected += affected
	}
	entry.updatedAt = time.Now()
	summary.TransactionPending = true
	if req.Commit {
		if err := m.commitEntryLocked(source.ID, req.SessionID, entry); err != nil {
			summary.TransactionPending = false
			return summary, err
		}
		summary.Committed = true
		summary.TransactionPending = false
	}
	summary.ElapsedMS = time.Since(started).Milliseconds()
	return summary, nil
}

func validGridSessionID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == ':' {
			continue
		}
		return false
	}
	return true
}
