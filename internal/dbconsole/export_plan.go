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
	SetIndices        []int    // SET 子句中包含的列索引
	UseRowID          bool     // 是否使用可信真实 ROWID 进行定位
	RowIDIndex        int      // ROWID 在 table.Columns 中的索引
	SourceFingerprint string
}

var (
	joinOrComplexRe = regexp.MustCompile(`(?is)\b(JOIN|UNION|GROUP\s+BY|HAVING)\b`)
	aliasRowIDRe    = regexp.MustCompile(`(?is)\bAS\s+["` + "`" + `]?ROWID["` + "`" + `]?\b`)
)

// ValidateSingleTableQuery 验证查询是否为适合生成 UPDATE 语句的安全单表查询
func ValidateSingleTableQuery(sql string) error {
	trimmed := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), "; \t\r\n"))
	if trimmed == "" {
		return errors.New("查询 SQL 不能为空")
	}
	if joinOrComplexRe.MatchString(trimmed) {
		return errors.New("JOIN、聚合或复合查询来源歧义，无法确定 UPDATE 目标基表，请选用 INSERT 或 CSV/JSON 格式")
	}
	// 检查 FROM 子句中是否有逗号分隔的多表 (笛卡尔积/隐式连接)
	match := fromTableRe.FindStringSubmatch(trimmed)
	if len(match) > 1 {
		rest := trimmed[strings.Index(trimmed, match[0])+len(match[0]):]
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

// BuildExportTargetPlan 构建并校验 UPDATE 导出的目标计划
func BuildExportTargetPlan(kind, tableName, querySQL string, columns []Column, pkCols []string, hasTrustedROWID bool) (ExportTargetPlan, error) {
	plan := ExportTargetPlan{
		Kind:       kind,
		RowIDIndex: -1,
	}

	tableName = strings.TrimSpace(tableName)
	if tableName == "" && querySQL != "" {
		tableName = InferExportTable(querySQL)
	}
	if tableName == "" || strings.EqualFold(tableName, "exported_rows") || strings.EqualFold(tableName, "DUAL") {
		return plan, errors.New("无法解析目标基表，请指定目标表名称或选用 INSERT/CSV 格式")
	}

	if querySQL != "" {
		if err := ValidateSingleTableQuery(querySQL); err != nil {
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
			isQuotedPK := len(pkTrim) >= 2 && pkTrim[0] == '"' && pkTrim[len(pkTrim)-1] == '"'
			cleanPK := strings.Trim(pkTrim, `"`+"`")
			matchIdx := -1

			for i, col := range columns {
				colTrim := strings.TrimSpace(col.Name)
				isQuotedCol := len(colTrim) >= 2 && colTrim[0] == '"' && colTrim[len(colTrim)-1] == '"'
				cleanCol := strings.Trim(colTrim, `"`+"`")

				if isQuotedPK || isQuotedCol {
					// 引号标识符要求严格大小写匹配
					if cleanCol == cleanPK {
						matchIdx = i
						break
					}
				} else {
					// 未加引号的普通标识符按方言规则进行大小写不敏感匹配
					if strings.EqualFold(cleanCol, cleanPK) {
						matchIdx = i
						break
					}
				}
			}

			if matchIdx < 0 {
				return plan, fmt.Errorf("缺少主键列 %s：目标表的主键必须完整包含在结果集中才能生成安全的 UPDATE 语句", pkTrim)
			}
			plan.KeyIndices = append(plan.KeyIndices, matchIdx)
			keyIndexMap[matchIdx] = struct{}{}
		}

		plan.PrimaryKeys = append([]string(nil), pkCols...)

		// 确定 SET 列：除主键以外的所有列；如果只有主键列，则 SET 与 WHERE 共享
		for i := range columns {
			if _, isPK := keyIndexMap[i]; !isPK {
				plan.SetIndices = append(plan.SetIndices, i)
			}
		}
		if len(plan.SetIndices) == 0 {
			plan.SetIndices = append([]int(nil), plan.KeyIndices...)
		}
	} else {
		// 使用 ROWID 时，除 ROWID 本身外的所有列都放入 SET
		for i := range columns {
			if i != plan.RowIDIndex {
				plan.SetIndices = append(plan.SetIndices, i)
			}
		}
		if len(plan.SetIndices) == 0 {
			plan.SetIndices = append(plan.SetIndices, plan.RowIDIndex)
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
