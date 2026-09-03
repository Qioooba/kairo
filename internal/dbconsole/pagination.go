package dbconsole

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
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

// serverPagedQuery builds the database-specific wrapper. The original query
// has already passed ValidateReadOnlySQL before this function is called.
func serverPagedQuery(kind, query string, page QueryPage) (string, error) {
	query = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), "; \t\r\n"))
	if query == "" || page.Page < 1 || page.PageSize < 1 {
		return "", fmt.Errorf("查询或分页参数无效")
	}
	tokens, _, err := sqlTokens(query)
	if err != nil {
		return "", fmt.Errorf("解析查询失败: %w", err)
	}
	if len(tokens) == 0 {
		return "", fmt.Errorf("解析查询失败: SQL 不能为空")
	}
	hasForUpdate := false
	for i := 0; i < len(tokens)-1; i++ {
		if tokens[i] == "FOR" && tokens[i+1] == "UPDATE" {
			hasForUpdate = true
			break
		}
	}
	if hasForUpdate {
		return query, nil
	}

	first := tokens[0]
	if first != "SELECT" && first != "WITH" {
		return query, nil
	}

	fetch := page.PageSize + 1
	if fetch <= page.PageSize {
		return "", fmt.Errorf("分页大小过大")
	}
	offset := page.Offset()
	if offset < 0 || offset > 1<<62-int64(fetch) {
		return "", fmt.Errorf("分页偏移量过大")
	}
	upper := offset + int64(fetch)
	digest := sha1.Sum([]byte(query))
	suffix := strings.ToLower(hex.EncodeToString(digest[:4]))
	rowAlias := `"__KAIRO_RN_` + suffix + `"`
	switch kind {
	case KindOracle:
		return fmt.Sprintf("SELECT * FROM (\nSELECT kairo_page_q.*, ROWNUM AS %s\nFROM (\n%s\n) kairo_page_q\nWHERE ROWNUM <= %d\n)\nWHERE %s > %d", rowAlias, query, upper, rowAlias, offset), nil
	case KindMySQL:
		return fmt.Sprintf("SELECT * FROM (\n%s\n) AS kairo_page_q LIMIT %d OFFSET %d", query, fetch, offset), nil
	default:
		return "", fmt.Errorf("%s 不是 SQL 数据源", kind)
	}
}

func queryHasOrderBy(query string) bool {
	tokens, _, err := sqlTokens(query)
	if err != nil {
		return false
	}
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] == "ORDER" && tokens[i+1] == "BY" {
			return true
		}
	}
	return false
}
