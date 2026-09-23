package dbconsole

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SingleTableQueryInfo 描述一个安全可改写的单表浏览查询
type SingleTableQueryInfo struct {
	Schema        string
	Table         string
	Alias         string
	TableAlias    string // 用于列投影前缀（带引号）
	WhereClause   string // WHERE 之后、ORDER BY 之前的内容
	OrderByClause string // ORDER BY 之后的内容
}

// parseSafeSingleTableQuery accepts only SELECT [alias.]* FROM [owner.]table [alias].
// Everything beyond that grammar is executed without projection rewriting.
func parseSafeSingleTableQuery(query string) (*SingleTableQueryInfo, bool) {
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	var tokens []string
	for i := 0; i < len(query); {
		c := query[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		if c == '.' || c == '*' {
			tokens = append(tokens, string(c))
			i++
			continue
		}
		start := i
		if c == '"' {
			i++
			closed := false
			for i < len(query) {
				if query[i] == '"' {
					i++
					if i < len(query) && query[i] == '"' {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, false
			}
		} else {
			for i < len(query) && ((query[i] >= 'a' && query[i] <= 'z') || (query[i] >= 'A' && query[i] <= 'Z') || (query[i] >= '0' && query[i] <= '9') || query[i] == '_' || query[i] == '$' || query[i] == '#') {
				i++
			}
			if i == start {
				return nil, false
			}
		}
		tokens = append(tokens, query[start:i])
	}
	if len(tokens) < 4 || !strings.EqualFold(tokens[0], "SELECT") {
		return nil, false
	}
	ident := func(s string) (gridIdentifier, bool) {
		parsed := parseGridIdentifierToken(KindOracle, s)
		if !parsed.known() {
			return gridIdentifier{}, false
		}
		switch parsed.Name {
		case "FROM", "WHERE", "ORDER", "AS", "JOIN", "SELECT", "FOR", "UNION", "GROUP":
			return gridIdentifier{}, false
		}
		if !parsed.Quoted {
			// 未加引号的标识符必须符合 Oracle 标识符词法（显式加引号的才允许任意字符）。
			for i, r := range parsed.Name {
				legal := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '$' || r == '#'
				if !legal {
					return gridIdentifier{}, false
				}
				if i == 0 && r >= '0' && r <= '9' {
					return gridIdentifier{}, false
				}
			}
		}
		return parsed, true
	}
	i := 1
	target := gridIdentifier{}
	if tokens[i] == "*" {
		i++
	} else {
		if len(tokens) < 6 || tokens[i+1] != "." || tokens[i+2] != "*" {
			return nil, false
		}
		var ok bool
		target, ok = ident(tokens[i])
		if !ok {
			return nil, false
		}
		i += 3
	}
	if i >= len(tokens) || !strings.EqualFold(tokens[i], "FROM") {
		return nil, false
	}
	i++
	if i >= len(tokens) {
		return nil, false
	}
	tableIdent, ok := ident(tokens[i])
	if !ok {
		return nil, false
	}
	i++
	info := &SingleTableQueryInfo{Table: tableIdent.normalized(KindOracle)}
	if i < len(tokens) && tokens[i] == "." {
		i++
		if i >= len(tokens) {
			return nil, false
		}
		info.Schema = tableIdent.normalized(KindOracle)
		tableIdent, ok = ident(tokens[i])
		if !ok {
			return nil, false
		}
		info.Table = tableIdent.normalized(KindOracle)
		i++
	}
	var aliasIdent gridIdentifier
	if i < len(tokens) {
		aliasIdent, ok = ident(tokens[i])
		if !ok {
			return nil, false
		}
		info.Alias = aliasIdent.Name
		i++
	}
	if i != len(tokens) {
		return nil, false
	}
	if info.Schema == "加载中…" || info.Schema == "加载中..." || info.Schema == "加载失败" || strings.Contains(info.Schema, "加载中") {
		info.Schema = ""
	}
	// 投影前缀必须与目标表或别名指向同一个对象（DB-01/DB-06：emp 与 "EMP" 相同，
	// 但 "emp" 是另一个区分大小写的对象，显式引号语义不能丢）。
	if target.known() {
		matches := gridIdentifierEqual(KindOracle, target, tableIdent)
		if !matches && aliasIdent.known() {
			matches = gridIdentifierEqual(KindOracle, target, aliasIdent)
		}
		if !matches {
			return nil, false
		}
	}
	// TableAlias 用于拼接物理列前缀，必须按方言规范化（未加引号的别名在 Oracle 中是大写）。
	qualifier := tableIdent
	if aliasIdent.known() {
		qualifier = aliasIdent
	}
	info.TableAlias, _ = quoteGridIdentifier(KindOracle, qualifier.normalized(KindOracle), "别名")
	if info.TableAlias == "" {
		return nil, false
	}
	return info, true
}

// LOBColumnProjection 描述单个重写后的列映射
type LOBColumnProjection struct {
	Original Column
	IsLOB    bool
	LOBKind  string // "clob" / "blob"
	LPIdx    int    // __LP_i 所在底层结果列序号
	LLIdx    int    // __LL_i 所在底层结果列序号
}

// LOBRewrittenQuery 编译完成的单表 LOB 投影重写信息
type LOBRewrittenQuery struct {
	SQL            string
	Columns        []Column
	Projections    []LOBColumnProjection
	HasLOBs        bool
	RowIDColIdx    int // __KAIRO_ROWID__ 在底层结果列中的序号
	PrimaryKeyCols []string
	PrimaryKeyIdxs map[string]int // PK列名在底层结果列中的序号
}

func (m *Manager) buildLOBProjectedQuery(ctx context.Context, source Source, info *SingleTableQueryInfo) (*LOBRewrittenQuery, error) {
	fields, err := m.Fields(ctx, source, info.Schema, info.Table)
	if err != nil || len(fields) == 0 {
		return nil, err
	}
	return compileLOBProjectedQuery(info, fields)
}

// Read metadata on the query's own connection: no nested pool/semaphore lease,
// and CURRENT_SCHEMA is resolved in the same Oracle session as the result.
func (m *Manager) buildLOBProjectionOn(ctx context.Context, q lobQueryer, sourceID string, info *SingleTableQueryInfo) (*LOBRewrittenQuery, error) {
	if info.Schema == "" {
		if err := q.QueryRowContext(ctx, `SELECT SYS_CONTEXT('USERENV','CURRENT_SCHEMA') FROM dual`).Scan(&info.Schema); err != nil {
			return nil, err
		}
	}
	cacheKey := fmt.Sprintf("%s\x00lob_proj_fields\x00%s\x00%s", sourceID, info.Schema, info.Table)
	if m != nil && sourceID != "" {
		if cachedFields, ok := metadataCacheGet[[]Field](m, cacheKey); ok {
			if len(cachedFields) == 0 {
				return nil, nil
			}
			return compileLOBProjectedQuery(info, cachedFields)
		}
	}
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM all_tables WHERE owner = :1 AND table_name = :2 AND temporary = 'N' AND iot_type IS NULL`, info.Schema, info.Table).Scan(&count); err != nil {
		return nil, err
	}
	if count != 1 {
		if m != nil && sourceID != "" {
			metadataCacheSet(m, cacheKey, []Field{})
		}
		return nil, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT c.column_name, c.data_type, c.nullable, c.column_id,
CASE WHEN EXISTS (SELECT 1 FROM all_constraints k JOIN all_cons_columns kc ON kc.owner=k.owner AND kc.constraint_name=k.constraint_name
WHERE k.owner=c.owner AND k.table_name=c.table_name AND k.constraint_type='P' AND k.status='ENABLED' AND kc.column_name=c.column_name) THEN 1 ELSE 0 END
FROM all_tab_columns c WHERE c.owner=:1 AND c.table_name=:2 AND c.column_id IS NOT NULL ORDER BY c.column_id`, info.Schema, info.Table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var fields []Field
	for rows.Next() {
		var f Field
		var nullable string
		var pk int
		if err := rows.Scan(&f.Name, &f.DataType, &nullable, &f.Ordinal, &pk); err != nil {
			return nil, err
		}
		f.Nullable, f.PrimaryKey = nullable == "Y", pk == 1
		fields = append(fields, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if m != nil && sourceID != "" {
		metadataCacheSet(m, cacheKey, append([]Field(nil), fields...))
	}
	return compileLOBProjectedQuery(info, fields)
}

// compileLOBProjectedQuery 将单表安全查询重写为包含 DBMS_LOB.GETLENGTH 与 ROWID 的显式投影（结论 2.2）。
// 若表内无 LOB 列，返回 nil 表示保持原样执行。
func compileLOBProjectedQuery(info *SingleTableQueryInfo, fields []Field) (*LOBRewrittenQuery, error) {
	hasLOB := false
	for _, f := range fields {
		if isLOBType(f.DataType) {
			hasLOB = true
			break
		}
	}
	if !hasLOB {
		return nil, nil // 无 LOB，无需重写
	}

	primaryKeyCols := make([]string, 0)
	for _, f := range fields {
		if f.PrimaryKey {
			primaryKeyCols = append(primaryKeyCols, f.Name)
		}
	}

	tableSQL, err := quoteGridIdentifier(KindOracle, info.Table, "表名")
	if err != nil {
		return nil, err
	}
	fromTarget := tableSQL
	if strings.TrimSpace(info.Schema) != "" {
		ownerSQL, oerr := quoteGridIdentifier(KindOracle, info.Schema, "schema")
		if oerr != nil {
			return nil, oerr
		}
		fromTarget = ownerSQL + "." + tableSQL
	}
	if info.Alias != "" {
		fromTarget += " " + info.TableAlias
	} else {
		fromTarget += " " + info.TableAlias
	}

	projParts := make([]string, 0, len(fields)*2+1)
	outColumns := make([]Column, 0, len(fields))
	projections := make([]LOBColumnProjection, 0, len(fields))
	pkIdxMap := make(map[string]int)

	underlyingColIdx := 0
	for i, f := range fields {
		colSQL, cerr := quoteGridIdentifier(KindOracle, f.Name, "列名")
		if cerr != nil {
			return nil, cerr
		}
		colExpr := info.TableAlias + "." + colSQL

		origCol := Column{
			Name:     f.Name,
			Database: f.DataType,
			Nullable: f.Nullable,
		}
		outColumns = append(outColumns, origCol)

		if f.PrimaryKey {
			pkIdxMap[strings.ToUpper(f.Name)] = underlyingColIdx
		}

		if isLOBType(f.DataType) {
			lobKind := "clob"
			if isBlobType(f.DataType) {
				lobKind = "blob"
			}
			lpAlias := fmt.Sprintf(`"__LP_%d"`, i)
			llAlias := fmt.Sprintf(`"__LL_%d"`, i)

			lpExpr := fmt.Sprintf("CASE WHEN %s IS NULL THEN 0 ELSE 1 END AS %s", colExpr, lpAlias)
			llExpr := fmt.Sprintf("CASE WHEN %s IS NULL THEN NULL ELSE DBMS_LOB.GETLENGTH(%s) END AS %s", colExpr, colExpr, llAlias)

			projParts = append(projParts, lpExpr, llExpr)

			projections = append(projections, LOBColumnProjection{
				Original: origCol,
				IsLOB:    true,
				LOBKind:  lobKind,
				LPIdx:    underlyingColIdx,
				LLIdx:    underlyingColIdx + 1,
			})
			underlyingColIdx += 2
		} else {
			projParts = append(projParts, colExpr)
			projections = append(projections, LOBColumnProjection{
				Original: origCol,
				IsLOB:    false,
				LPIdx:    underlyingColIdx,
			})
			underlyingColIdx++
		}
	}

	// 辅助列：ROWIDTOCHAR(alias.ROWID) 用于构建 LOB 定位 Token 与稳定排序兜底
	rowIDIdx := underlyingColIdx
	projParts = append(projParts, fmt.Sprintf("ROWIDTOCHAR(%s.ROWID) AS \"__KAIRO_ROWID__\"", info.TableAlias))
	underlyingColIdx++

	// 确定性排序（结论 2.5）：仅在用户显式指定 ORDER BY 时追加主键或 ROWID 保证稳定；
	// 若用户未指定 ORDER BY，严禁强行追加全表排序（避免千万级大表全表排序 SORT ORDER BY 耗费数十秒）
	stableOrderBy := ""
	if strings.TrimSpace(info.OrderByClause) != "" {
		stableOrderBy = "ORDER BY " + info.OrderByClause
		if len(primaryKeyCols) > 0 {
			var pkParts []string
			for _, pk := range primaryKeyCols {
				pkQ, _ := quoteGridIdentifier(KindOracle, pk, "主键")
				pkParts = append(pkParts, info.TableAlias+"."+pkQ)
			}
			stableOrderBy += ", " + strings.Join(pkParts, ", ")
		} else {
			stableOrderBy += ", " + info.TableAlias + ".ROWID"
		}
	}

	wherePart := ""
	if strings.TrimSpace(info.WhereClause) != "" {
		wherePart = "WHERE " + info.WhereClause
	}

	compiledSQL := fmt.Sprintf("SELECT\n  %s\nFROM %s\n%s\n%s",
		strings.Join(projParts, ",\n  "),
		fromTarget,
		wherePart,
		stableOrderBy,
	)

	return &LOBRewrittenQuery{
		SQL:            compiledSQL,
		Columns:        outColumns,
		Projections:    projections,
		HasLOBs:        true,
		RowIDColIdx:    rowIDIdx,
		PrimaryKeyCols: primaryKeyCols,
		PrimaryKeyIdxs: pkIdxMap,
	}, nil
}

// lobRowScanner 负责扫描重写后的投影并为 LOB 列构建虚拟单元格与签名 Token
type lobRowScanner struct {
	plan              *LOBRewrittenQuery
	rawScanners       []any
	rawDests          []any
	sourceID          string
	sourceFingerprint string
	dbUser            string
	owner             string
	table             string
	sessionID         string
	aliasIdx          int // pagination alias column index if any
}

func newLOBRowScanner(plan *LOBRewrittenQuery, source Source, owner, table, sessionID string, aliasIdx int) *lobRowScanner {
	colCount := len(plan.Projections) + 2 // normal cols + 1 extra for LOB expansion + rowid
	if plan.RowIDColIdx >= colCount {
		colCount = plan.RowIDColIdx + 1
	}
	if aliasIdx >= colCount {
		colCount = aliasIdx + 1
	}

	rawScanners := make([]any, colCount)
	rawDests := make([]any, colCount)

	for i := range rawScanners {
		var val any
		rawScanners[i] = &val
		rawDests[i] = &val
	}

	return &lobRowScanner{
		plan:              plan,
		rawScanners:       rawScanners,
		rawDests:          rawDests,
		sourceID:          source.ID,
		sourceFingerprint: SourceFingerprint(source),
		dbUser:            source.Username,
		owner:             owner,
		table:             table,
		sessionID:         sessionID,
		aliasIdx:          aliasIdx,
	}
}

func (s *lobRowScanner) Scan(rows *sql.Rows) ([]any, int64, error) {
	if err := rows.Scan(s.rawDests...); err != nil {
		return nil, 0, err
	}

	getRawVal := func(idx int) any {
		if idx < 0 || idx >= len(s.rawScanners) {
			return nil
		}
		p, ok := s.rawScanners[idx].(*any)
		if ok && p != nil {
			return *p
		}
		return nil
	}

	// 提取行 ROWID
	rowID := ""
	if rVal := getRawVal(s.plan.RowIDColIdx); rVal != nil {
		switch v := rVal.(type) {
		case string:
			rowID = v
		case []byte:
			rowID = string(v)
		default:
			rowID = fmt.Sprint(v)
		}
	}

	// 提取主键值
	rowKeys := make(map[string]any, len(s.plan.PrimaryKeyCols))
	for _, pk := range s.plan.PrimaryKeyCols {
		if idx, ok := s.plan.PrimaryKeyIdxs[strings.ToUpper(pk)]; ok {
			rowKeys[pk] = getRawVal(idx)
		}
	}

	// 构建对外呈现的列数据
	outRow := make([]any, len(s.plan.Projections))
	for i, proj := range s.plan.Projections {
		if !proj.IsLOB {
			outRow[i] = normalizeColumnValue(getRawVal(proj.LPIdx), proj.Original.Database)
			continue
		}

		// LOB 列：从 __LP_i 与 __LL_i 组装虚拟单元格
		presentVal := getRawVal(proj.LPIdx)
		lenVal := getRawVal(proj.LLIdx)

		isPresent := false
		if presentVal != nil {
			switch v := presentVal.(type) {
			case int64:
				isPresent = v != 0
			case int:
				isPresent = v != 0
			case float64:
				isPresent = v != 0
			case []byte:
				isPresent = len(v) > 0 && v[0] == '1'
			case string:
				isPresent = v == "1"
			default:
				isPresent = fmt.Sprint(v) == "1"
			}
		}

		if !isPresent {
			outRow[i] = nil
			continue
		}

		var lobLen int64 = 0
		if lenVal != nil {
			switch v := lenVal.(type) {
			case int64:
				lobLen = v
			case int:
				lobLen = int64(v)
			case float64:
				lobLen = int64(v)
			case string:
				fmt.Sscanf(v, "%d", &lobLen)
			case []byte:
				fmt.Sscanf(string(v), "%d", &lobLen)
			default:
				lobLen, _ = strconv.ParseInt(fmt.Sprint(v), 10, 64)
			}
		}

		token, tokenErr := GenerateSignedLOBTokenWithFingerprint(
			s.sourceID,
			s.sourceFingerprint,
			s.dbUser,
			s.owner,
			s.table,
			proj.Original.Name,
			proj.Original.Database,
			rowID,
			rowKeys,
			s.plan.PrimaryKeyCols,
			s.sessionID,
			5*time.Minute,
		)
		if tokenErr != nil {
			return nil, 0, tokenErr
		}

		outRow[i] = map[string]any{
			"kind":          proj.LOBKind,
			"database_type": proj.Original.Database,
			"length_unit":   map[bool]string{true: "bytes", false: "characters"}[proj.LOBKind == "blob"],
			"name":          proj.Original.Name,
			"cell_type":     "lob",
			"present":       true,
			"length":        lobLen,
			"display":       fmt.Sprintf("(%s)", proj.Original.Database),
			"token":         token,
			"truncated":     false,
		}
	}

	// DB-06: 与普通 scanner 返回完全相同的协议 —— 在行尾追加真实 ROWID，
	// 使编辑计划广告的 hidden_rowid_index 等于最终序列化后的下标。
	// 只有确实交付了行身份载荷，后端才允许声明可编辑。
	if s.plan.RowIDColIdx >= 0 {
		outRow = append(outRow, rowID)
	}

	return outRow, fastRowBytes(outRow), nil
}
