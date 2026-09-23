package dbconsole

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ExportTargetPlan 描述 UPDATE/INSERT 等导出的目标基表与行定位计划
type ExportTargetPlan struct {
	Kind              string   // KindOracle, KindMySQL
	Schema            string   // 目标表所属 Schema / Owner
	Table             string   // 目标表名
	FullTarget        string   // 带引号的完整表名 (用于 SQL 语句)
	PrimaryKeys       []string // 必须匹配的所有主键列名
	KeyIndices        []int    // 每个主键列在 table.Columns 中的索引 (必须全长对应)
	KeyPhysicalNames  []string // 每个主键列对应的目标表物理列名 (用于 WHERE)
	SetIndices        []int    // SET 子句中包含的列索引
	SetPhysicalNames  []string // 每个 SET 列对应的目标表物理列名 (用于 SET)
	UseRowID          bool     // 是否使用可信真实 ROWID 进行定位
	RowIDIndex        int      // ROWID 在 table.Columns 中的索引
	SourceFingerprint string
}

var (
	joinOrComplexRe = regexp.MustCompile(`(?is)\b(JOIN|UNION|GROUP\s+BY|HAVING)\b`)
	aliasRowIDRe    = regexp.MustCompile(`(?is)\bAS\s+["` + "`" + `]?ROWID["` + "`" + `]?\b`)
)

// ValidateSingleTableQuery 验证查询是否为适合生成 UPDATE 语句的安全单表查询
func ValidateSingleTableQuery(kind, sql string) error {
	trimmed := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), "; \t\r\n"))
	if trimmed == "" {
		return errors.New("查询 SQL 不能为空")
	}
	noComments := stripSQLComments(kind, trimmed)
	if regexp.MustCompile(`(?is)^\s*WITH\b`).MatchString(noComments) {
		return errors.New("CTE 查询无法确定 UPDATE 目标基表，请选用 INSERT 或 CSV/JSON 格式")
	}
	if regexp.MustCompile(`(?is)\bFROM\s*\(`).MatchString(noComments) {
		return errors.New("派生表或子查询来源歧义，无法确定 UPDATE 目标基表，请选用 INSERT 或 CSV/JSON 格式")
	}
	if joinOrComplexRe.MatchString(noComments) {
		return errors.New("JOIN、聚合或复合查询来源歧义，无法确定 UPDATE 目标基表，请选用 INSERT 或 CSV/JSON 格式")
	}
	// 检查 FROM 子句中是否有逗号分隔的多表 (笛卡尔积/隐式连接)
	match := fromTableRe.FindStringSubmatch(noComments)
	if len(match) > 1 {
		rest := noComments[strings.Index(noComments, match[0])+len(match[0]):]
		// 如果在 WHERE/ORDER/GROUP/LIMIT 之前存在逗号，说明有多表连接
		beforeClause := rest
		for _, sep := range []string{"WHERE", "ORDER", "GROUP", "LIMIT", "FOR"} {
			if idx := strings.Index(strings.ToUpper(beforeClause), sep); idx >= 0 {
				beforeClause = beforeClause[:idx]
			}
		}
		if strings.Contains(beforeClause, ",") {
			return errors.New("多表笛卡尔积或隐式连接查询无法确定 UPDATE 目标基表，请选用 INSERT 或 CSV/JSON 格式")
		}
	}
	return nil
}

func isSpaceOrPunct(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '(' || c == ')' || c == ',' || c == ';'
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '`' && s[len(s)-1] == '`')) {
		return true
	}
	for i, r := range s {
		if i == 0 {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_') {
				return false
			}
		} else {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '$' || r == '#') {
				return false
			}
		}
	}
	return true
}

var reservedSQLKeywords = map[string]bool{
	"NULL":              true,
	"TRUE":              true,
	"FALSE":             true,
	"UNKNOWN":           true,
	"CURRENT_DATE":      true,
	"CURRENT_TIME":      true,
	"CURRENT_TIMESTAMP": true,
	"SYSDATE":           true,
	"SYSTIMESTAMP":      true,
	"USER":              true,
	"ROWNUM":            true,
	"LEVEL":             true,
}

// stripSQLComments removes /* ... */ and -- comments while preserving quoted strings.
//
// P1（审核第 6 项）：`#` 只是 MySQL 的行注释起始符。Oracle 的未加引号标识符允许包含 `#`
// （Oracle 11g SQL 语言参考的命名规则，例如 ORDERS#ARCHIVE），无条件把 `#` 当注释会把
// 表名截断成 ORDERS，导出 UPDATE 时就会写到另一张表。因此按方言区分。
func stripSQLComments(kind, sql string) string {
	hashStartsComment := kind == KindMySQL
	var b strings.Builder
	b.Grow(len(sql))
	n := len(sql)
	inSingle := false
	inDouble := false
	inBacktick := false

	for i := 0; i < n; i++ {
		c := sql[i]
		if inSingle {
			b.WriteByte(c)
			if c == '\'' {
				if i+1 < n && sql[i+1] == '\'' {
					b.WriteByte(sql[i+1])
					i++
				} else {
					inSingle = false
				}
			}
			continue
		}
		if inDouble {
			b.WriteByte(c)
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if inBacktick {
			b.WriteByte(c)
			if c == '`' {
				inBacktick = false
			}
			continue
		}

		if c == '\'' {
			inSingle = true
			b.WriteByte(c)
			continue
		}
		if c == '"' {
			inDouble = true
			b.WriteByte(c)
			continue
		}
		if c == '`' {
			inBacktick = true
			b.WriteByte(c)
			continue
		}

		// Block comment
		if c == '/' && i+1 < n && sql[i+1] == '*' {
			i += 2
			for i+1 < n && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			i++ // skip '/'
			b.WriteByte(' ')
			continue
		}
		// Line comments: -- everywhere, # 仅 MySQL
		if (c == '-' && i+1 < n && sql[i+1] == '-') || (hashStartsComment && c == '#') {
			for i < n && sql[i] != '\n' && sql[i] != '\r' {
				i++
			}
			b.WriteByte(' ')
			continue
		}

		b.WriteByte(c)
	}
	return b.String()
}

// extractSelectProjections extracts the raw projection items between SELECT and FROM.
func extractSelectProjections(kind, sql string) ([]string, error) {
	clean := strings.TrimSpace(stripSQLComments(kind, sql))
	clean = strings.TrimRight(clean, "; \t\r\n")
	upper := strings.ToUpper(clean)
	if !strings.HasPrefix(upper, "SELECT") {
		return nil, errors.New("仅支持以 SELECT 开头的单表查询导出 UPDATE 脚本")
	}

	inSingle := false
	inDouble := false
	inBacktick := false
	depth := 0
	fromIdx := -1
	n := len(clean)

	for i := 6; i < n; i++ {
		c := clean[i]
		if inSingle {
			if c == '\'' {
				if i+1 < n && clean[i+1] == '\'' {
					i++
				} else {
					inSingle = false
				}
			}
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if inBacktick {
			if c == '`' {
				inBacktick = false
			}
			continue
		}

		if c == '\'' {
			inSingle = true
			continue
		}
		if c == '"' {
			inDouble = true
			continue
		}
		if c == '`' {
			inBacktick = true
			continue
		}

		if c == '(' {
			depth++
			continue
		}
		if c == ')' {
			depth--
			continue
		}

		if depth == 0 {
			if i+4 <= n && strings.EqualFold(clean[i:i+4], "FROM") {
				prev := clean[i-1]
				next := byte(' ')
				if i+4 < n {
					next = clean[i+4]
				}
				if isSpaceOrPunct(prev) && isSpaceOrPunct(next) {
					fromIdx = i
					break
				}
			}
		}
	}

	if fromIdx < 0 {
		return nil, errors.New("无法定位查询的 FROM 子句，不能确定 UPDATE 目标基表")
	}

	projStr := strings.TrimSpace(clean[6:fromIdx])
	upperProj := strings.ToUpper(projStr)
	if strings.HasPrefix(upperProj, "DISTINCT") && len(projStr) > 8 && isSpaceOrPunct(projStr[8]) {
		projStr = strings.TrimSpace(projStr[8:])
	} else if strings.HasPrefix(upperProj, "ALL") && len(projStr) > 3 && isSpaceOrPunct(projStr[3]) {
		projStr = strings.TrimSpace(projStr[3:])
	}

	var items []string
	start := 0
	depth = 0
	inSingle = false
	inDouble = false
	inBacktick = false
	for i := 0; i < len(projStr); i++ {
		c := projStr[i]
		if inSingle {
			if c == '\'' {
				if i+1 < len(projStr) && projStr[i+1] == '\'' {
					i++
				} else {
					inSingle = false
				}
			}
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if inBacktick {
			if c == '`' {
				inBacktick = false
			}
			continue
		}

		if c == '\'' {
			inSingle = true
			continue
		}
		if c == '"' {
			inDouble = true
			continue
		}
		if c == '`' {
			inBacktick = true
			continue
		}

		if c == '(' {
			depth++
			continue
		}
		if c == ')' {
			depth--
			continue
		}

		if c == ',' && depth == 0 {
			items = append(items, strings.TrimSpace(projStr[start:i]))
			start = i + 1
		}
	}
	if start < len(projStr) {
		items = append(items, strings.TrimSpace(projStr[start:]))
	}

	return items, nil
}

func parseProjectionItem(kind, item string) (physName, alias string, isWildcard bool, err error) {
	trimmed := strings.TrimSpace(item)
	if trimmed == "" {
		return "", "", false, errors.New("投影列不能为空")
	}
	if trimmed == "*" || strings.HasSuffix(trimmed, ".*") {
		return "", "", true, nil
	}

	inSingle := false
	inDouble := false
	inBacktick := false
	hasParen := false
	hasOperator := false

	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if inSingle {
			if c == '\'' {
				if i+1 < len(trimmed) && trimmed[i+1] == '\'' {
					i++
				} else {
					inSingle = false
				}
			}
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if inBacktick {
			if c == '`' {
				inBacktick = false
			}
			continue
		}

		if c == '\'' {
			inSingle = true
			continue
		}
		if c == '"' {
			inDouble = true
			continue
		}
		if c == '`' {
			inBacktick = true
			continue
		}

		if c == '(' || c == ')' {
			hasParen = true
		}
		if c == '+' || c == '-' || c == '*' || c == '/' || c == '%' ||
			c == '&' || c == '|' || c == '^' || c == '~' || c == '!' ||
			c == '<' || c == '>' || c == '=' {
			hasOperator = true
		}
	}

	if hasParen {
		return "", "", false, fmt.Errorf("列投影 %q 包含函数或子查询表达式，无法证明基表物理列来源，不能生成安全的 UPDATE 脚本", item)
	}
	if hasOperator {
		return "", "", false, fmt.Errorf("列投影 %q 包含计算表达式，无法证明基表物理列来源，不能生成安全的 UPDATE 脚本", item)
	}

	sourcePart := trimmed
	aliasPart := ""

	asIdx := -1
	inSingle = false
	inDouble = false
	inBacktick = false
	for i := 0; i+3 < len(trimmed); i++ {
		c := trimmed[i]
		if inSingle {
			if c == '\'' {
				if i+1 < len(trimmed) && trimmed[i+1] == '\'' {
					i++
				} else {
					inSingle = false
				}
			}
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if inBacktick {
			if c == '`' {
				inBacktick = false
			}
			continue
		}
		if c == '\'' {
			inSingle = true
			continue
		}
		if c == '"' {
			inDouble = true
			continue
		}
		if c == '`' {
			inBacktick = true
			continue
		}

		if (i == 0 || isSpaceOrPunct(trimmed[i-1])) &&
			strings.EqualFold(trimmed[i:i+2], "AS") &&
			isSpaceOrPunct(trimmed[i+2]) {
			asIdx = i
			break
		}
	}

	if asIdx >= 0 {
		sourcePart = strings.TrimSpace(trimmed[:asIdx])
		aliasPart = strings.TrimSpace(trimmed[asIdx+2:])
	} else {
		lastSpace := -1
		inSingle = false
		inDouble = false
		inBacktick = false
		for i := 0; i < len(trimmed); i++ {
			c := trimmed[i]
			if inSingle {
				if c == '\'' {
					if i+1 < len(trimmed) && trimmed[i+1] == '\'' {
						i++
					} else {
						inSingle = false
					}
				}
				continue
			}
			if inDouble {
				if c == '"' {
					inDouble = false
				}
				continue
			}
			if inBacktick {
				if c == '`' {
					inBacktick = false
				}
				continue
			}
			if c == '\'' {
				inSingle = true
				continue
			}
			if c == '"' {
				inDouble = true
				continue
			}
			if c == '`' {
				inBacktick = true
				continue
			}
			if c == ' ' || c == '\t' {
				lastSpace = i
			}
		}
		if lastSpace >= 0 {
			sourcePart = strings.TrimSpace(trimmed[:lastSpace])
			aliasPart = strings.TrimSpace(trimmed[lastSpace+1:])
		}
	}

	dotIdx := -1
	inSingle = false
	inDouble = false
	inBacktick = false
	for i := len(sourcePart) - 1; i >= 0; i-- {
		c := sourcePart[i]
		if c == '"' || c == '`' || c == '\'' {
			continue
		}
		if c == '.' {
			dotIdx = i
			break
		}
	}
	colName := sourcePart
	if dotIdx >= 0 {
		colName = strings.TrimSpace(sourcePart[dotIdx+1:])
	}

	if strings.HasPrefix(colName, "'") || (len(colName) > 0 && colName[0] >= '0' && colName[0] <= '9') {
		return "", "", false, fmt.Errorf("列投影 %q 为常量，无法证明基表物理列来源，不能生成安全的 UPDATE 脚本", item)
	}

	cleanCol := strings.Trim(colName, `"`+"`")
	if !isIdent(colName) || cleanCol == "" {
		return "", "", false, fmt.Errorf("列投影 %q 格式非法，无法证明基表物理列来源", item)
	}
	if reservedSQLKeywords[strings.ToUpper(cleanCol)] {
		return "", "", false, fmt.Errorf("列投影 %q 为系统保留字或伪列，不能生成安全的 UPDATE 脚本", item)
	}

	physName = cleanCol
	if !isQuotedIdent(colName) && kind == KindOracle {
		physName = strings.ToUpper(cleanCol)
	}

	if aliasPart != "" {
		cleanAlias := strings.Trim(aliasPart, `"`+"`")
		if !isIdent(aliasPart) || cleanAlias == "" {
			return "", "", false, fmt.Errorf("列别名 %q 格式非法", aliasPart)
		}
		alias = cleanAlias
	} else {
		alias = cleanCol
	}

	return physName, alias, false, nil
}

// BuildExportTargetPlan 构建并校验 UPDATE 导出的目标计划
func BuildExportTargetPlan(kind, tableName, querySQL string, columns []Column, pkCols []string, hasTrustedROWID bool) (ExportTargetPlan, error) {
	plan := ExportTargetPlan{
		Kind:       kind,
		RowIDIndex: -1,
	}

	tableName = strings.TrimSpace(tableName)
	if tableName == "" && querySQL != "" {
		tableName = InferExportTable(kind, querySQL)
	}
	if tableName == "" || strings.EqualFold(tableName, "exported_rows") || strings.EqualFold(tableName, "DUAL") {
		return plan, errors.New("无法解析目标基表，请指定目标表名称或选用 INSERT/CSV 格式")
	}

	if querySQL != "" {
		if err := ValidateSingleTableQuery(kind, querySQL); err != nil {
			return plan, err
		}
	}

	schema, obj := SplitSchemaObject(tableName)
	plan.Schema = schema
	plan.Table = obj
	plan.FullTarget = QuoteIdent(kind, tableName)

	if len(columns) == 0 {
		return plan, errors.New("没有可导出的列")
	}

	// 解析并验证 SQL 投影列，获取基表物理列映射
	physicalNames := make([]string, len(columns))
	if querySQL != "" {
		items, err := extractSelectProjections(kind, querySQL)
		if err != nil {
			return plan, err
		}
		if len(items) == 1 && (items[0] == "*" || strings.HasSuffix(items[0], ".*")) {
			for i, col := range columns {
				c := strings.Trim(col.Name, `"`+"`")
				if kind == KindOracle && !isQuotedIdent(col.Name) {
					c = strings.ToUpper(c)
				}
				physicalNames[i] = c
			}
		} else {
			if len(items) != len(columns) {
				return plan, errors.New("查询投影列数与结果集列数不一致，无法确定物理列映射")
			}
			for i, item := range items {
				phys, alias, isWild, err := parseProjectionItem(kind, item)
				if err != nil {
					return plan, err
				}
				if isWild {
					return plan, errors.New("不支持通配符与显式列混合投影")
				}
				cleanCol := strings.Trim(columns[i].Name, `"`+"`")
				if !strings.EqualFold(cleanCol, alias) {
					return plan, fmt.Errorf("结果列 %q 与查询投影 %q 别名不匹配", columns[i].Name, item)
				}
				physicalNames[i] = phys
			}
		}
	} else {
		for i, col := range columns {
			c := strings.Trim(col.Name, `"`+"`")
			if kind == KindOracle && !isQuotedIdent(col.Name) {
				c = strings.ToUpper(c)
			}
			physicalNames[i] = c
		}
	}

	// 1. 检查真实单表 ROWID
	// 只有经后端单表查询证明且非 "expr AS ROWID" 别名的真实物理行标识才允许使用
	isForgedRowID := querySQL != "" && aliasRowIDRe.MatchString(querySQL)
	if hasTrustedROWID && !isForgedRowID {
		for i, col := range columns {
			clean := strings.Trim(col.Name, `"`+"`")
			if strings.EqualFold(clean, "ROWID") {
				plan.UseRowID = true
				plan.RowIDIndex = i
				break
			}
		}
	}

	// 2. 如果不使用 ROWID，则必须具备完整的主键约束与结果映射
	if !plan.UseRowID {
		if len(pkCols) == 0 {
			return plan, fmt.Errorf("目标表 %q 未定义主键且无安全物理行标识，不能生成安全的 UPDATE 脚本；请选用 INSERT 或 CSV/JSON 格式", tableName)
		}

		keyIndexMap := make(map[int]struct{})
		for _, pk := range pkCols {
			pkTrim := strings.TrimSpace(pk)
			if pkTrim == "" {
				continue
			}
			cleanPK := strings.Trim(pkTrim, `"`+"`")
			if kind == KindOracle && !isQuotedIdent(pkTrim) {
				cleanPK = strings.ToUpper(cleanPK)
			}
			var matchIdxs []int

			for i, col := range columns {
				cleanCol := strings.Trim(col.Name, `"`+"`")
				if kind == KindOracle && !isQuotedIdent(col.Name) {
					cleanCol = strings.ToUpper(cleanCol)
				}
				phys := physicalNames[i]

				if strings.EqualFold(phys, cleanPK) {
					matchIdxs = append(matchIdxs, i)
				} else if strings.EqualFold(cleanCol, cleanPK) {
					return plan, fmt.Errorf("列别名 %q 伪装为主键列 %s，无法证明基表物理列来源", col.Name, pkTrim)
				}
			}

			if len(matchIdxs) == 0 {
				return plan, fmt.Errorf("缺少主键列 %s：目标表的主键必须完整包含在结果集中才能生成安全的 UPDATE 语句", pkTrim)
			}
			if len(matchIdxs) > 1 {
				return plan, fmt.Errorf("主键列 %s 存在重复投影，无法确定唯一物理主键映射", pkTrim)
			}
			matchIdx := matchIdxs[0]
			plan.KeyIndices = append(plan.KeyIndices, matchIdx)
			plan.KeyPhysicalNames = append(plan.KeyPhysicalNames, physicalNames[matchIdx])
			keyIndexMap[matchIdx] = struct{}{}
		}

		plan.PrimaryKeys = append([]string(nil), pkCols...)

		// 确定 SET 列：除主键以外的所有列；如果只有主键列，则 SET 与 WHERE 共享
		for i := range columns {
			if _, isPK := keyIndexMap[i]; !isPK {
				plan.SetIndices = append(plan.SetIndices, i)
				plan.SetPhysicalNames = append(plan.SetPhysicalNames, physicalNames[i])
			}
		}
		if len(plan.SetIndices) == 0 {
			plan.SetIndices = append([]int(nil), plan.KeyIndices...)
			plan.SetPhysicalNames = append([]string(nil), plan.KeyPhysicalNames...)
		}
	} else {
		// 使用 ROWID 时，除 ROWID 本身外的所有列都放入 SET
		for i := range columns {
			if i != plan.RowIDIndex {
				plan.SetIndices = append(plan.SetIndices, i)
				plan.SetPhysicalNames = append(plan.SetPhysicalNames, physicalNames[i])
			}
		}
		if len(plan.SetIndices) == 0 {
			plan.SetIndices = append(plan.SetIndices, plan.RowIDIndex)
			plan.SetPhysicalNames = append(plan.SetPhysicalNames, physicalNames[plan.RowIDIndex])
		}
	}

	return plan, nil
}

// ValidateRowValues 检查单行数据的定位值和更新值是否完整有效
func (p ExportTargetPlan) ValidateRowValues(columns []Column, row []any, rowIndex int) error {
	// 校验定位键 (WHERE 列)
	whereIndices := p.KeyIndices
	if p.UseRowID && p.RowIDIndex >= 0 {
		whereIndices = []int{p.RowIDIndex}
	}

	for _, idx := range whereIndices {
		colName := columns[idx].Name
		var val any
		if idx < len(row) {
			val = row[idx]
		}
		if val == nil {
			return fmt.Errorf("行定位失败：第 %d 行的主键列 %s 值为 NULL，无法生成确定性 WHERE 条件", rowIndex+1, colName)
		}
		if obj, ok := val.(map[string]any); ok {
			if lazy, _ := obj["lazy"].(bool); lazy {
				return fmt.Errorf("行定位失败：第 %d 行的主键列 %s 为未完整加载的 LOB 对象", rowIndex+1, colName)
			}
			if kind, _ := obj["kind"].(string); kind == "clob" || kind == "blob" {
				return fmt.Errorf("行定位失败：第 %d 行的主键列 %s 为 LOB 对象", rowIndex+1, colName)
			}
			if trunc, _ := obj["truncated"].(bool); trunc {
				return fmt.Errorf("行定位失败：第 %d 行的主键列 %s 包含截断值，不能作为定位标识", rowIndex+1, colName)
			}
			if kind, _ := obj["kind"].(string); kind == "binary" {
				return fmt.Errorf("行定位失败：第 %d 行的主键列 %s 为二进制对象", rowIndex+1, colName)
			}
		}
	}

	// 校验更新值 (SET 列)
	for _, idx := range p.SetIndices {
		colName := columns[idx].Name
		var val any
		if idx < len(row) {
			val = row[idx]
		}
		if obj, ok := val.(map[string]any); ok {
			if lazy, _ := obj["lazy"].(bool); lazy {
				return fmt.Errorf("数据未完整：第 %d 行的列 %s 为未加载的 LOB 对象，无法生成 SQL 字面量；请先加载或选用 CSV 格式", rowIndex+1, colName)
			}
			if trunc, _ := obj["truncated"].(bool); trunc {
				return fmt.Errorf("数据未完整：第 %d 行的列 %s 为截断预览值，不能作为完整 SQL 字面量导出", rowIndex+1, colName)
			}
			if kind, _ := obj["kind"].(string); kind == "binary" {
				return fmt.Errorf("不支持的类型：第 %d 行的列 %s 为二进制内容，无法直接生成 SQL 字面量，请选用文件导出或 CSV", rowIndex+1, colName)
			}
		}
	}
	return nil
}
