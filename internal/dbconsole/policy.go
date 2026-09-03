package dbconsole

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// SQLStatementInfo describes the classified SQL statement.
type SQLStatementInfo struct {
	Type         string `json:"type"`          // "SELECT", "FOR_UPDATE", "DML", "DDL", "TRANSACTION", "COMMAND", "STATEMENT"
	Action       string `json:"action"`        // "SELECT", "UPDATE", "INSERT", "DELETE", "CREATE", "ALTER", "DROP", "TRUNCATE", ...
	IsQuery      bool   `json:"is_query"`      // true if statement produces a result set (SELECT, FOR UPDATE, SHOW, DESC, EXPLAIN)
	HasForUpdate bool   `json:"has_for_update"` // true if query contains FOR UPDATE
}

// ClassifySQL analyzes the given SQL statement and categorizes it.
func ClassifySQL(kind, query string) (SQLStatementInfo, error) {
	tokens, semicolonContent, err := sqlTokens(query)
	if err != nil {
		return SQLStatementInfo{}, err
	}
	if len(tokens) == 0 {
		return SQLStatementInfo{}, errors.New("SQL 不能为空")
	}
	if semicolonContent {
		return SQLStatementInfo{}, errors.New("单次仅允许执行单条 SQL 语句")
	}

	// Reject dangerous MySQL host filesystem writes / reads and sleep/lock attacks
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t == "INTO" && i+1 < len(tokens) && (tokens[i+1] == "OUTFILE" || tokens[i+1] == "DUMPFILE") {
			return SQLStatementInfo{}, errors.New("禁止写文件查询 (INTO OUTFILE/DUMPFILE)")
		}
		if t == "LOAD_FILE" {
			return SQLStatementInfo{}, errors.New("禁止使用 LOAD_FILE 函数")
		}
		if t == "SLEEP" || t == "BENCHMARK" || t == "GET_LOCK" || t == "RELEASE_LOCK" {
			return SQLStatementInfo{}, fmt.Errorf("禁止使用 %s 函数", t)
		}
	}

	hasForUpdate := false
	for i := 0; i < len(tokens)-1; i++ {
		if tokens[i] == "FOR" && tokens[i+1] == "UPDATE" {
			hasForUpdate = true
			break
		}
	}

	first := tokens[0]
	switch first {
	case "SELECT", "WITH":
		if hasForUpdate {
			return SQLStatementInfo{Type: "FOR_UPDATE", Action: "FOR UPDATE", IsQuery: true, HasForUpdate: true}, nil
		}
		return SQLStatementInfo{Type: "SELECT", Action: first, IsQuery: true, HasForUpdate: false}, nil
	case "SHOW", "DESC", "DESCRIBE", "EXPLAIN":
		return SQLStatementInfo{Type: "COMMAND", Action: first, IsQuery: true, HasForUpdate: false}, nil
	case "UPDATE", "INSERT", "DELETE", "MERGE", "REPLACE":
		return SQLStatementInfo{Type: "DML", Action: first, IsQuery: false, HasForUpdate: false}, nil
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME":
		return SQLStatementInfo{Type: "DDL", Action: first, IsQuery: false, HasForUpdate: false}, nil
	case "COMMIT", "ROLLBACK":
		return SQLStatementInfo{Type: "TRANSACTION", Action: first, IsQuery: false, HasForUpdate: false}, nil
	default:
		return SQLStatementInfo{}, fmt.Errorf("不支持或禁止执行此类型语句: %s", first)
	}
}

// ValidateSQL classifies and validates a SQL statement for developer workbench execution.
func ValidateSQL(kind, query string) (SQLStatementInfo, error) {
	return ClassifySQL(kind, query)
}

// ValidateReadOnlySQL is kept for callers requiring strictly read-only execution.
func ValidateReadOnlySQL(kind, query string) error {
	tokens, semicolonContent, err := sqlTokens(query)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return errors.New("SQL 不能为空")
	}
	if semicolonContent {
		return errors.New("一期仅允许执行单条 SQL")
	}
	first := tokens[0]
	allowed := first == "SELECT" || first == "WITH"
	if kind == KindMySQL {
		allowed = allowed || first == "SHOW" || first == "DESC" || first == "DESCRIBE" || first == "EXPLAIN"
	}
	if !allowed {
		return errors.New("一期仅允许只读查询（SELECT/WITH；MySQL 另支持 SHOW/DESC/EXPLAIN）")
	}
	denied := map[string]struct{}{
		"INSERT": {}, "UPDATE": {}, "DELETE": {}, "MERGE": {}, "REPLACE": {},
		"CREATE": {}, "ALTER": {}, "DROP": {}, "TRUNCATE": {}, "RENAME": {},
		"GRANT": {}, "REVOKE": {}, "COMMIT": {}, "ROLLBACK": {}, "CALL": {},
		"EXEC": {}, "EXECUTE": {}, "LOCK": {}, "ANALYZE": {},
		"LOAD_FILE": {}, "GET_LOCK": {}, "RELEASE_LOCK": {}, "SLEEP": {}, "BENCHMARK": {},
	}
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if _, blocked := denied[t]; blocked {
			return errors.New("查询包含写入、DDL 或执行型关键字，已被只读策略拒绝")
		}
		if t == "FOR" && i+1 < len(tokens) && tokens[i+1] == "UPDATE" {
			return errors.New("禁止 SELECT FOR UPDATE")
		}
		if t == "INTO" && i+1 < len(tokens) && (tokens[i+1] == "OUTFILE" || tokens[i+1] == "DUMPFILE") {
			return errors.New("禁止写文件查询")
		}
	}
	return nil
}

func sqlTokens(query string) ([]string, bool, error) {
	var tokens []string
	var word strings.Builder
	state := byte(0) // 0 normal, ', ", `, line comment L, block comment B
	semicolon := false
	contentAfterSemicolon := false
	flush := func() {
		if word.Len() > 0 {
			tokens = append(tokens, strings.ToUpper(word.String()))
			word.Reset()
		}
	}
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch state {
		case 'L':
			if c == '\n' {
				state = 0
			}
			continue
		case 'B':
			if c == '*' && i+1 < len(query) && query[i+1] == '/' {
				state = 0
				i++
			}
			continue
		case '\'', '"', '`':
			if c == state {
				if i+1 < len(query) && query[i+1] == state {
					i++
					continue
				}
				state = 0
			} else if c == '\\' && i+1 < len(query) {
				i++
			}
			continue
		}
		if c == '-' && i+1 < len(query) && query[i+1] == '-' {
			flush()
			state = 'L'
			i++
			continue
		}
		if c == '/' && i+1 < len(query) && query[i+1] == '*' {
			flush()
			if i+2 < len(query) && query[i+2] == '!' {
				return nil, false, errors.New("禁止 MySQL 可执行版本注释 /*! ... */")
			}
			state = 'B'
			i++
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			flush()
			if semicolon {
				contentAfterSemicolon = true
			}
			state = c
			continue
		}
		if c == ';' {
			flush()
			if semicolon {
				contentAfterSemicolon = true
			}
			semicolon = true
			continue
		}
		if unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) || c == '_' || c == '$' || c >= 0x80 {
			if semicolon {
				contentAfterSemicolon = true
			}
			word.WriteByte(c)
		} else {
			flush()
			if semicolon && !unicode.IsSpace(rune(c)) {
				contentAfterSemicolon = true
			}
		}
	}
	flush()
	if state == '\'' || state == '"' || state == '`' || state == 'B' {
		return nil, false, errors.New("SQL 中存在未闭合的字符串、标识符或注释")
	}
	return tokens, contentAfterSemicolon, nil
}
