package dbconsole

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// QueryPage describes the visible page requested by the UI. A page is bounded
// by Source.MaxRows; offset is calculated server-side.
type QueryPage struct {
	Page     int
	PageSize int
}

func normalizeQueryPage(source Source, page, pageSize int) QueryPage {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > source.MaxRows {
		pageSize = source.MaxRows
	}
	return QueryPage{Page: page, PageSize: pageSize}
}

func (p QueryPage) Offset() int64 {
	if p.Page <= 1 || p.PageSize <= 0 {
		return 0
	}
	return int64(p.Page-1) * int64(p.PageSize)
}

// PaginationPlan 描述服务端分页改写计划与辅助列元数据
type PaginationPlan struct {
	SQL              string
	HasHelperColumn  bool
	HelperColumnName string // 无引号列名，如 __KAIRO_RN_xxxx
}

// serverPagedPlan builds the database-specific pagination plan.
func serverPagedPlan(kind, query string, page QueryPage) (PaginationPlan, error) {
	plan := PaginationPlan{}
	query = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), "; \t\r\n"))
	if query == "" || page.Page < 1 || page.PageSize < 1 {
		return plan, fmt.Errorf("查询或分页参数无效")
	}
	// Bound both wrapped OFFSET queries and cursor-skipped command pages.
	if int64(page.Page-1) > 10000000/int64(page.PageSize) {
		return plan, fmt.Errorf("分页偏移量超过 1000 万行，请缩小查询范围")
	}
	tokens, _, err := sqlTokensDialect(kind, query)
	if err != nil {
		return plan, fmt.Errorf("解析查询失败: %w", err)
	}
	if len(tokens) == 0 {
		return plan, fmt.Errorf("解析查询失败: SQL 不能为空")
	}
	hasForUpdate := false
	for i := 0; i < len(tokens)-1; i++ {
		if tokens[i] == "FOR" && tokens[i+1] == "UPDATE" {
			hasForUpdate = true
			break
		}
	}
	if hasForUpdate {
		plan.SQL = query
		return plan, nil
	}

	first := tokens[0]
	if first != "SELECT" && first != "WITH" {
		plan.SQL = query
		return plan, nil
	}

	fetch := page.PageSize + 1
	if fetch <= page.PageSize {
		return plan, fmt.Errorf("分页大小过大")
	}
	offset := page.Offset()
	if offset < 0 || offset > 1<<62-int64(fetch) {
		return plan, fmt.Errorf("分页偏移量过大")
	}
	upper := offset + int64(fetch)
	digest := sha1.Sum([]byte(query))
	suffix := strings.ToLower(hex.EncodeToString(digest[:4]))
	rawColName := "__KAIRO_RN_" + suffix
	rowAlias := `"` + rawColName + `"`
	switch kind {
	case KindOracle:
		if offset == 0 {
			plan.HasHelperColumn = false
			if fetch <= 100 {
				plan.SQL = fmt.Sprintf("SELECT /*+ FIRST_ROWS(%d) */ * FROM (\n%s\n) WHERE ROWNUM <= %d", fetch, query, upper)
				return plan, nil
			}
			plan.SQL = fmt.Sprintf("SELECT * FROM (\n%s\n) WHERE ROWNUM <= %d", query, upper)
			return plan, nil
		}
		plan.HasHelperColumn = true
		plan.HelperColumnName = rawColName
		plan.SQL = fmt.Sprintf("SELECT * FROM (\nSELECT kairo_page_q.*, ROWNUM AS %s\nFROM (\n%s\n) kairo_page_q\nWHERE ROWNUM <= %d\n)\nWHERE %s > %d", rowAlias, query, upper, rowAlias, offset)
		return plan, nil
	case KindMySQL:
		plan.HasHelperColumn = false
		if offset == 0 {
			plan.SQL = fmt.Sprintf("SELECT * FROM (\n%s\n) AS kairo_page_q LIMIT %d", query, fetch)
			return plan, nil
		}
		plan.SQL = fmt.Sprintf("SELECT * FROM (\n%s\n) AS kairo_page_q LIMIT %d OFFSET %d", query, fetch, offset)
		return plan, nil
	default:
		return plan, fmt.Errorf("%s 不是 SQL 数据源", kind)
	}
}

// serverPagedQuery builds the database-specific wrapper. The original query
// has already passed ValidateReadOnlySQL before this function is called.
func serverPagedQuery(kind, query string, page QueryPage) (string, error) {
	plan, err := serverPagedPlan(kind, query, page)
	return plan.SQL, err
}

// QueryHasOrderBy reports whether query contains a top-level ORDER BY clause.
func QueryHasOrderBy(query string) bool {
	return queryHasTopLevelOrderBy("", query)
}

func queryHasOrderBy(query string) bool {
	return QueryHasOrderBy(query)
}

// QueryHasTopLevelOrderBy reports whether query contains a top-level ORDER BY clause,
// properly distinguishing from window function OVER (ORDER BY) or subqueries.
func QueryHasTopLevelOrderBy(kind, query string) bool {
	return queryHasTopLevelOrderBy(kind, query)
}

func queryHasTopLevelOrderBy(kind, query string) bool {
	var word strings.Builder
	parenDepth := 0
	state := byte(0) // 0 normal, ', ", `, 'L' line comment, 'B' block comment
	lastWordAtZero := ""

	flush := func() bool {
		if word.Len() > 0 {
			w := strings.ToUpper(word.String())
			word.Reset()
			if parenDepth == 0 {
				if lastWordAtZero == "ORDER" && w == "BY" {
					return true
				}
				lastWordAtZero = w
			}
		}
		return false
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
			} else if c == '\\' && kind == KindMySQL && state != '`' && i+1 < len(query) {
				i++
			}
			continue
		}

		if kind == KindOracle && (c == 'q' || c == 'Q') && i+2 < len(query) && query[i+1] == '\'' && word.Len() == 0 {
			closer := query[i+2]
			switch closer {
			case '[':
				closer = ']'
			case '{':
				closer = '}'
			case '(':
				closer = ')'
			case '<':
				closer = '>'
			}
			end := strings.Index(query[i+3:], string([]byte{closer, '\''}))
			if end < 0 {
				return false
			}
			i += end + 4
			continue
		}

		if kind == KindMySQL && c == '#' {
			if flush() {
				return true
			}
			state = 'L'
			continue
		}
		if c == '-' && i+1 < len(query) && query[i+1] == '-' && (kind != KindMySQL || i+2 == len(query) || query[i+2] <= ' ') {
			if flush() {
				return true
			}
			state = 'L'
			i++
			continue
		}
		if c == '/' && i+1 < len(query) && query[i+1] == '*' {
			if flush() {
				return true
			}
			state = 'B'
			i++
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			if flush() {
				return true
			}
			state = c
			continue
		}
		if c == '(' {
			if flush() {
				return true
			}
			parenDepth++
			lastWordAtZero = ""
			continue
		}
		if c == ')' {
			if flush() {
				return true
			}
			if parenDepth > 0 {
				parenDepth--
			}
			lastWordAtZero = ""
			continue
		}
		if c == ';' {
			if flush() {
				return true
			}
			lastWordAtZero = ""
			continue
		}
		if unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) || c == '_' || c == '$' || c >= 0x80 {
			word.WriteByte(c)
		} else {
			if flush() {
				return true
			}
		}
	}
	if flush() {
		return true
	}
	return false
}

