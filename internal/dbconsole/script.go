package dbconsole

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxScriptBytes      = 1 << 20
	maxScriptStatements = 100
)

// BindParameter is the wire-safe representation of one SQL parameter.  Value
// is intentionally any so the JSON protocol can carry a string/number/bool;
// Type tells the server how to convert it before handing it to database/sql.
type BindParameter struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // string/text/int/int64/uint/float/bool/date/datetime/timestamp/bytes/json/null
	Value any    `json:"value,omitempty"`
	Null  bool   `json:"null,omitempty"`
}

// ScriptOptions controls transaction and failure semantics.  The defaults are
// deliberately conservative: one local transaction for a DML-only script,
// rollback on the first failure, and no implicit commit of a tab transaction.
type ScriptOptions struct {
	FailurePolicy   string `json:"failure_policy,omitempty"`   // rollback | stop | continue
	TransactionMode string `json:"transaction_mode,omitempty"` // auto | single | none | session
	Commit          bool   `json:"commit,omitempty"`           // only standalone local tx
}

type ScriptStatement struct {
	Index        int              `json:"index"`
	SQL          string           `json:"sql"`
	Type         SQLStatementInfo `json:"statement"`
	Status       string           `json:"status"` // pending/running/succeeded/failed/skipped
	RowsAffected int64            `json:"rows_affected,omitempty"`
	ElapsedMS    int64            `json:"elapsed_ms,omitempty"`
	Error        string           `json:"error,omitempty"`
}

type ScriptResult struct {
	Statements         []ScriptStatement `json:"statements"`
	RowsAffected       int64             `json:"rows_affected"`
	ElapsedMS          int64             `json:"elapsed_ms"`
	Committed          bool              `json:"committed"`
	RolledBack         bool              `json:"rolled_back"`
	TransactionPending bool              `json:"transaction_pending"`
	NonAtomic          bool              `json:"non_atomic,omitempty"`
	Message            string            `json:"message,omitempty"`
}

var bindNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$#]*$`)
var decimalBindRE = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// SplitSQLScript separates ordinary semicolon-delimited statements and Oracle
// PL/SQL blocks.  In a PL/SQL block semicolons are part of the block; a line
// containing only '/' terminates it (SQL*Plus/SQL Developer convention).
// Quotes and both SQL comment forms are handled lexically, so semicolons in a
// string or comment do not split the script.
func SplitSQLScript(kind, script string) ([]string, error) {
	return splitSQLScript(kind, script, maxScriptStatements, maxScriptBytes)
}

func splitSQLScript(kind, script string, maxStatements, maxBytes int) ([]string, error) {
	if len(script) == 0 {
		return nil, errors.New("脚本不能为空")
	}
	if len(script) > maxBytes {
		return nil, fmt.Errorf("脚本超过 %d 字节限制", maxBytes)
	}
	if maxStatements <= 0 {
		maxStatements = maxScriptStatements
	}
	var out []string
	start, started, blockMode := 0, false, false
	flush := func(end int) error {
		value := strings.TrimSpace(script[start:end])
		if started && value != "" {
			if len(out) >= maxStatements {
				return fmt.Errorf("脚本语句数量超过 %d 条限制", maxStatements)
			}
			out = append(out, value)
		}
		started, blockMode = false, false
		return nil
	}
	for i := 0; i < len(script); {
		c := script[i]
		if strings.ContainsRune(" \t\r\n", rune(c)) {
			i++
			continue
		}
		end, comment, err := sqlRegionEnd(kind, script, i)
		if err != nil {
			return nil, err
		}
		if comment {
			i = end
			continue
		}
		if !started && c != ';' {
			started = true
			blockMode = strings.EqualFold(kind, KindOracle) && isPLSQLBlockStart(script[i:])
		}
		if end > i {
			i = end
			continue
		}
		if c == '/' && blockMode {
			lineStart := strings.LastIndexByte(script[:i], '\n') + 1
			lineEnd := strings.IndexByte(script[i:], '\n')
			if lineEnd < 0 {
				lineEnd = len(script)
			} else {
				lineEnd += i
			}
			if strings.TrimSpace(script[lineStart:lineEnd]) == "/" {
				if err := flush(lineStart); err != nil {
					return nil, err
				}
				i, start = lineEnd, lineEnd
				continue
			}
		}
		if c == ';' && !blockMode {
			if err := flush(i); err != nil {
				return nil, err
			}
			start = i + 1
		}
		i++
	}
	if err := flush(len(script)); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("脚本没有可执行语句")
	}
	return out, nil
}

// sqlRegionEnd is shared by script splitting and binding. Quotes/comments
// must have exactly the same boundaries in both operations.
func sqlRegionEnd(kind, text string, i int) (int, bool, error) {
	if strings.HasPrefix(text[i:], "--") || kind == KindMySQL && text[i] == '#' {
		if end := strings.IndexByte(text[i:], '\n'); end >= 0 {
			return i + end + 1, true, nil
		}
		return len(text), true, nil
	}
	if strings.HasPrefix(text[i:], "/*") {
		if strings.HasPrefix(text[i:], "/*!") {
			return 0, false, errors.New("禁止 MySQL 可执行版本注释")
		}
		if end := strings.Index(text[i+2:], "*/"); end >= 0 {
			return i + end + 4, true, nil
		}
		return 0, false, errors.New("SQL 注释未闭合")
	}
	if kind == KindOracle && i+2 < len(text) && (text[i] == 'q' || text[i] == 'Q') && text[i+1] == '\'' {
		close := text[i+2]
		switch close {
		case '[':
			close = ']'
		case '{':
			close = '}'
		case '(':
			close = ')'
		case '<':
			close = '>'
		}
		for j := i + 3; j+1 < len(text); j++ {
			if text[j] == close && text[j+1] == '\'' {
				return j + 2, false, nil
			}
		}
		return 0, false, errors.New("Oracle q-quote 未闭合")
	}
	quote := text[i]
	if quote != '\'' && quote != '"' && quote != '`' {
		return i, false, nil
	}
	for j := i + 1; j < len(text); j++ {
		if text[j] == quote {
			if j+1 < len(text) && text[j+1] == quote {
				j++
				continue
			}
			return j + 1, false, nil
		}
		if kind == KindMySQL && text[j] == '\\' {
			j++
		}
	}
	return 0, false, errors.New("SQL 字符串或标识符未闭合")
}

func leadingSQLWords(text string) []string {
	var words []string
	for i := 0; i < len(text) && len(words) < 5; {
		if strings.ContainsRune(" \t\r\n", rune(text[i])) {
			i++
			continue
		}
		end, comment, err := sqlRegionEnd(KindOracle, text, i)
		if err != nil {
			break
		}
		if comment {
			i = end
			continue
		}
		if end > i || !isBindNameChar(text[i]) {
			break
		}
		j := i + 1
		for j < len(text) && isBindNameChar(text[j]) {
			j++
		}
		words = append(words, strings.ToUpper(text[i:j]))
		i = j
	}
	return words
}

func scriptBlockAction(text string) string {
	words := leadingSQLWords(text)
	if len(words) == 0 {
		return ""
	}
	if words[0] == "BEGIN" || words[0] == "DECLARE" {
		return words[0]
	}
	if words[0] != "CREATE" {
		return ""
	}
	i := 1
	if len(words) > 2 && words[1] == "OR" && words[2] == "REPLACE" {
		i = 3
	}
	if i < len(words) && (words[i] == "EDITIONABLE" || words[i] == "NONEDITIONABLE") {
		i++
	}
	if i >= len(words) {
		return ""
	}
	switch words[i] {
	case "FUNCTION", "PROCEDURE", "PACKAGE", "TRIGGER", "TYPE":
		return "CREATE " + words[i]
	}
	return ""
}

func isPLSQLBlockStart(text string) bool { return scriptBlockAction(text) != "" }

func ClassifyScriptSQL(kind, statement string) (SQLStatementInfo, error) {
	if strings.EqualFold(strings.TrimSpace(kind), KindOracle) {
		if action := scriptBlockAction(statement); action != "" {
			typ := "PLSQL"
			if strings.HasPrefix(action, "CREATE ") {
				typ = "DDL"
			}
			return SQLStatementInfo{Type: typ, Action: action}, nil
		}
	}
	return ClassifySQL(kind, strings.TrimSpace(statement))
}

func normalizeScriptOptions(options ScriptOptions) (ScriptOptions, error) {
	options.FailurePolicy = strings.ToLower(strings.TrimSpace(options.FailurePolicy))
	if options.FailurePolicy == "" {
		options.FailurePolicy = "rollback"
	}
	if options.FailurePolicy != "rollback" && options.FailurePolicy != "stop" && options.FailurePolicy != "continue" {
		return options, errors.New("failure_policy 仅支持 rollback / stop / continue")
	}
	options.TransactionMode = strings.ToLower(strings.TrimSpace(options.TransactionMode))
	if options.TransactionMode == "" {
		options.TransactionMode = "auto"
	}
	if options.TransactionMode != "auto" && options.TransactionMode != "single" && options.TransactionMode != "none" && options.TransactionMode != "session" {
		return options, errors.New("transaction_mode 仅支持 auto / single / none / session")
	}
	return options, nil
}

// ExecuteScript executes a bounded script and reports every statement's
// result.  A session id stages DML in the tab transaction; a standalone DML
// script uses one local transaction unless mode=none.  Scripts containing DDL
// are intentionally non-atomic because Oracle/MySQL may implicitly commit.
func (m *Manager) ExecuteScript(ctx context.Context, source Source, script, sessionID string, params []BindParameter, options ScriptOptions) (ScriptResult, error) {
	if !source.MutationAllowed() {
		return ScriptResult{}, errors.New("该数据源处于只读锁定状态")
	}
	options, err := normalizeScriptOptions(options)
	if err != nil {
		return ScriptResult{}, err
	}
	statements, err := SplitSQLScript(source.Kind, script)
	if err != nil {
		return ScriptResult{}, err
	}
	result := ScriptResult{Statements: make([]ScriptStatement, len(statements))}
	infos := make([]SQLStatementInfo, len(statements))
	hasDDL := false
	if _, _, err := BindSQLParameters(source.Kind, script, params); err != nil {
		return ScriptResult{}, err
	}
	type boundStatement struct {
		sql  string
		args []any
	}
	bound := make([]boundStatement, len(statements))
	positionalOffset := 0
	for i, stmt := range statements {
		info, infoErr := ClassifyScriptSQL(source.Kind, stmt)
		if infoErr != nil {
			return ScriptResult{}, fmt.Errorf("第 %d 条语句: %w", i+1, infoErr)
		}
		if info.Type == "TRANSACTION" {
			return ScriptResult{}, fmt.Errorf("脚本第 %d 条包含事务控制；请使用页签提交/回滚或脚本事务选项", i+1)
		}
		if info.Type == "PLSQL" {
			return ScriptResult{}, fmt.Errorf("脚本第 %d 条是匿名 PL/SQL 块；为防止绕过 DML/DDL 门禁，脚本执行接口不允许匿名块", i+1)
		}
		if info.IsQuery {
			return ScriptResult{}, fmt.Errorf("脚本第 %d 条是查询；请使用查询接口获取结果集", i+1)
		}
		if info.Type == "DDL" {
			if !source.DDLAllowed() {
				return ScriptResult{}, fmt.Errorf("脚本第 %d 条包含 DDL，但数据源未开启 DDL 能力或处于只读锁定状态", i+1)
			}
			hasDDL = true
		}
		boundSQL, args, bindErr := bindSQLParametersAt(source.Kind, stmt, params, true, &positionalOffset)
		if bindErr != nil {
			return ScriptResult{}, fmt.Errorf("第 %d 条语句: %w", i+1, bindErr)
		}
		bound[i] = boundStatement{sql: boundSQL, args: args}
		infos[i] = info
		result.Statements[i] = ScriptStatement{Index: i, SQL: stmt, Type: info, Status: "pending", RowsAffected: -1}
	}
	if options.TransactionMode == "session" && strings.TrimSpace(sessionID) == "" {
		return ScriptResult{}, errors.New("transaction_mode=session 必须绑定 session_id")
	}
	if sessionID != "" && hasDDL {
		if m.SessionTransactionPending(source, sessionID) {
			return ScriptResult{}, errors.New("当前页签有未提交 DML；请先提交/回滚后单独执行 DDL")
		}
	}
	if hasDDL && options.TransactionMode == "single" {
		return ScriptResult{}, errors.New("包含 DDL 的脚本不能使用 single 原子事务")
	}
	queryCtx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(queryCtx); err != nil {
		return ScriptResult{}, err
	}
	defer m.release()
	db, err := m.sqlDB(source)
	if err != nil {
		return ScriptResult{}, err
	}
	started := time.Now()
	var tx *sql.Tx
	var entry *transactionEntry
	if sessionID != "" {
		entry, err = m.transactionForContext(queryCtx, source, sessionID, true)
		if err != nil {
			return ScriptResult{}, err
		}
		entry.mu.Lock()
		defer entry.mu.Unlock()
		tx = entry.tx
	} else if options.TransactionMode == "single" || options.TransactionMode == "auto" && !hasDDL {
		tx, err = db.BeginTx(queryCtx, nil)
		if err != nil {
			return ScriptResult{}, err
		}
		defer tx.Rollback()
	}
	if tx == nil && hasDDL {
		result.NonAtomic = true
	}
	var failed bool
	for i := range result.Statements {
		item := &result.Statements[i]
		item.Status = "running"
		startedStmt := time.Now()
		execSQL, args := bound[i].sql, bound[i].args
		var execResult sql.Result
		if tx != nil {
			execResult, err = tx.ExecContext(queryCtx, execSQL, args...)
		} else {
			execResult, err = db.ExecContext(queryCtx, execSQL, args...)
		}
		item.ElapsedMS = time.Since(startedStmt).Milliseconds()
		if err != nil {
			item.Status, item.Error = "failed", err.Error()
			failed = true
			if options.FailurePolicy != "continue" {
				break
			}
			continue
		}
		item.Status = "succeeded"
		if execResult != nil {
			if affected, affectedErr := execResult.RowsAffected(); affectedErr == nil {
				item.RowsAffected = affected
				if affected >= 0 {
					result.RowsAffected += affected
				}
			}
		}
	}
	for i := range result.Statements {
		if result.Statements[i].Status == "pending" {
			result.Statements[i].Status = "skipped"
		}
	}
	if failed && (options.FailurePolicy == "rollback" || queryCtx.Err() != nil || isConnectionFailure(err)) {
		if tx != nil {
			_ = tx.Rollback()
			result.RolledBack = true
			if entry != nil {
				if entry.cancel != nil {
					entry.cancel()
				}
				m.removeTransaction(source.ID, sessionID, entry)
			}
		}
	} else if tx != nil && entry == nil {
		if failed && options.FailurePolicy == "stop" {
			_ = tx.Rollback()
			result.RolledBack = true
		} else if options.Commit || options.TransactionMode == "single" || options.TransactionMode == "auto" {
			if commitErr := tx.Commit(); commitErr != nil {
				return result, commitErr
			}
			result.Committed = true
		}
	} else if entry != nil {
		entry.updatedAt = time.Now()
		result.TransactionPending = true
	}
	result.ElapsedMS = time.Since(started).Milliseconds()
	if failed {
		result.Message = "脚本执行存在失败语句"
	} else if result.TransactionPending {
		result.Message = "脚本已暂存到页签事务，等待提交"
	} else {
		result.Message = "脚本执行成功"
	}
	if failed {
		return result, errors.New("脚本执行失败")
	}
	return result, nil
}

// BindSQLParameters validates and converts typed wire parameters.  Oracle
// keeps named binds (database/sql.Named), while MySQL receives positional '?'
// arguments because its driver does not implement named placeholders.
func BindSQLParameters(kind, statement string, params []BindParameter) (string, []any, error) {
	return bindSQLParameters(kind, statement, params, false)
}

func bindSQLParameters(kind, statement string, params []BindParameter, allowUnused bool) (string, []any, error) {
	return bindSQLParametersAt(kind, statement, params, allowUnused, nil)
}

func bindSQLParametersAt(kind, statement string, params []BindParameter, allowUnused bool, position *int) (string, []any, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	lookup := make(map[string]BindParameter, len(params))
	for _, p := range params {
		name := strings.TrimSpace(p.Name)
		if name == "" || !bindNameRE.MatchString(name) && !isNumericBind(name) {
			return "", nil, fmt.Errorf("绑定参数名 %q 非法", p.Name)
		}
		key := strings.ToUpper(name)
		if _, exists := lookup[key]; exists {
			return "", nil, fmt.Errorf("绑定参数 %q 重复", name)
		}
		converted, err := convertBindValue(p)
		if err != nil {
			return "", nil, fmt.Errorf("绑定参数 %s: %w", name, err)
		}
		p.Value = converted
		lookup[key] = p
	}
	var out strings.Builder
	var args []any
	used := map[string]bool{}
	positional := 0
	if position != nil {
		positional = *position
		defer func() { *position = positional }()
	}
	for i := 0; i < len(statement); i++ {
		c := statement[i]
		end, _, err := sqlRegionEnd(kind, statement, i)
		if err != nil {
			return "", nil, err
		}
		if end > i {
			out.WriteString(statement[i:end])
			i = end - 1
			continue
		}
		if c == ':' {
			if i+1 < len(statement) && statement[i+1] == ':' {
				out.WriteString("::")
				i++
				continue
			}
			j := i + 1
			for j < len(statement) && (isBindNameChar(statement[j])) {
				j++
			}
			if j > i+1 {
				name := statement[i+1 : j]
				value, ok := lookup[strings.ToUpper(name)]
				if !ok {
					return "", nil, fmt.Errorf("缺少绑定参数 :%s", name)
				}
				used[strings.ToUpper(name)] = true
				if strings.EqualFold(strings.TrimSpace(kind), KindMySQL) {
					out.WriteByte('?')
					args = append(args, value.Value)
				} else {
					out.WriteByte(':')
					out.WriteString(name)
					args = append(args, sql.Named(name, value.Value))
				}
				i = j - 1
				continue
			}
		}
		if c == '?' {
			positional++
			name := strconv.Itoa(positional)
			value, ok := lookup[name]
			if !ok {
				return "", nil, fmt.Errorf("缺少第 %d 个位置参数", positional)
			}
			used[name] = true
			if strings.EqualFold(strings.TrimSpace(kind), KindMySQL) {
				out.WriteByte('?')
				args = append(args, value.Value)
			} else {
				bindName := "kairo_pos_" + name
				for {
					if _, exists := lookup[strings.ToUpper(bindName)]; !exists {
						break
					}
					bindName += "_"
				}
				out.WriteString(":" + bindName)
				args = append(args, sql.Named(bindName, value.Value))
			}
			continue
		}
		out.WriteByte(c)
	}
	for key := range lookup {
		if !allowUnused && !used[key] {
			return "", nil, fmt.Errorf("绑定参数 %s 未在 SQL 中使用", key)
		}
	}
	return out.String(), args, nil
}

func isNumericBind(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isBindNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$' || c == '#'
}

func convertBindValue(p BindParameter) (any, error) {
	if p.Null || strings.EqualFold(strings.TrimSpace(p.Type), "null") {
		return nil, nil
	}
	typ := strings.ToLower(strings.TrimSpace(p.Type))
	if typ == "" {
		typ = "string"
	}
	if typ == "json" {
		if p.Value == nil {
			return nil, nil
		}
		if raw, ok := p.Value.(string); ok {
			var value any
			if err := json.Unmarshal([]byte(raw), &value); err != nil {
				return nil, fmt.Errorf("JSON 值非法: %w", err)
			}
			return raw, nil
		}
		if raw, err := json.Marshal(p.Value); err == nil {
			return string(raw), nil
		}
		return nil, errors.New("JSON 值无法编码")
	}
	switch typ {
	case "string", "text":
		return fmt.Sprint(p.Value), nil
	case "int", "int64":
		switch value := p.Value.(type) {
		case float64:
			if value != float64(int64(value)) {
				return nil, errors.New("整数值不能包含小数")
			}
			return int64(value), nil
		case json.Number:
			return strconv.ParseInt(string(value), 10, 64)
		default:
			return strconv.ParseInt(strings.TrimSpace(fmt.Sprint(value)), 10, 64)
		}
	case "uint":
		return strconv.ParseUint(strings.TrimSpace(fmt.Sprint(p.Value)), 10, 64)
	case "decimal":
		value := strings.TrimSpace(fmt.Sprint(p.Value))
		if !decimalBindRE.MatchString(value) {
			return nil, errors.New("decimal 值格式非法")
		}
		return value, nil // preserve decimal precision through the driver
	case "float", "float64", "number":
		return strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(p.Value)), 64)
	case "bool", "boolean":
		if value, ok := p.Value.(bool); ok {
			return value, nil
		}
		return strconv.ParseBool(strings.TrimSpace(fmt.Sprint(p.Value)))
	case "date":
		return parseBindTime(p.Value, []string{"2006-01-02"})
	case "datetime", "timestamp":
		return parseBindTime(p.Value, []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"})
	case "bytes", "blob", "binary":
		value, ok := p.Value.(string)
		if !ok {
			return nil, errors.New("bytes 类型必须使用 Base64 字符串")
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("Base64 无效: %w", err)
		}
		if len(decoded) > maxCellBytes {
			return nil, fmt.Errorf("bytes 值超过 %d 字节", maxCellBytes)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("不支持的参数类型 %q", p.Type)
	}
}

func parseBindTime(value any, layouts []string) (time.Time, error) {
	text := strings.TrimSpace(fmt.Sprint(value))
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("时间值 %q 格式非法", text)
}
