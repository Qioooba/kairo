package dbconsole

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// GridColumnBinding 描述查询结果列到物理基表列的映射关系与编辑权限
type GridColumnBinding struct {
	Index          int    `json:"index"`
	ResultName     string `json:"result_name"`
	PhysicalName   string `json:"physical_name"`
	DataType       string `json:"data_type"`
	IsPrimaryKey   bool   `json:"is_primary_key"`
	IsNullable     bool   `json:"is_nullable"`
	Writable       bool   `json:"writable"`
	ReadOnlyReason string `json:"read_only_reason,omitempty"`
}

// ResultEditContext 维护单次查询执行的不可变来源、对象与行定位上下文
type ResultEditContext struct {
	ResultID          string              `json:"result_id"`
	SessionID         string              `json:"session_id"`
	SourceID          string              `json:"source_id"`
	SourceFingerprint string              `json:"source_fingerprint"`
	Dialect           string              `json:"dialect"` // "oracle", "mysql"
	ExecutedSQL       string              `json:"executed_sql"`
	Schema            string              `json:"schema"`
	Table             string              `json:"table"`
	TableAlias        string              `json:"table_alias"`
	Columns           []GridColumnBinding `json:"columns"`
	PrimaryKeys       []string            `json:"primary_keys"`    // 服务端元数据确认的完整有效主键物理列名
	UniqueKeys        []string            `json:"unique_keys"`     // 服务端元数据确认的完整非空唯一键物理列名
	IdentityPolicy    string              `json:"identity_policy"` // "pk", "oracle_rowid", "unique", "none"
	HiddenRowIDIndex  int                 `json:"hidden_rowid_index"`
	CanInsert         bool                `json:"can_insert"`
	CanUpdate         bool                `json:"can_update"`
	CanDelete         bool                `json:"can_delete"`
	Reason            string              `json:"reason,omitempty"`
	HasTopLevelOrder  bool                `json:"has_top_level_order"`
	CreatedAt         time.Time           `json:"created_at"`
}

// GridEditPlanSummary 序列化返回给前端的只读能力摘要
type GridEditPlanSummary struct {
	ResultID         string   `json:"result_id"`
	Schema           string   `json:"schema"`
	Table            string   `json:"table"`
	IdentityPolicy   string   `json:"identity_policy"` // "pk", "oracle_rowid", "unique", "none"
	CanInsert        bool     `json:"can_insert"`
	CanUpdate        bool     `json:"can_update"`
	CanDelete        bool     `json:"can_delete"`
	Reason           string   `json:"reason,omitempty"`
	PrimaryKeys      []string `json:"primary_keys,omitempty"`
	UniqueKeys       []string `json:"unique_keys,omitempty"`
	HiddenRowIDIndex int      `json:"hidden_rowid_index,omitempty"`
	HasTopLevelOrder bool     `json:"has_top_level_order"`
}

func (ctx *ResultEditContext) ToSummary() *GridEditPlanSummary {
	if ctx == nil {
		return nil
	}
	return &GridEditPlanSummary{
		ResultID:         ctx.ResultID,
		Schema:           ctx.Schema,
		Table:            ctx.Table,
		IdentityPolicy:   ctx.IdentityPolicy,
		CanInsert:        ctx.CanInsert,
		CanUpdate:        ctx.CanUpdate,
		CanDelete:        ctx.CanDelete,
		Reason:           ctx.Reason,
		PrimaryKeys:      append([]string(nil), ctx.PrimaryKeys...),
		UniqueKeys:       append([]string(nil), ctx.UniqueKeys...),
		HiddenRowIDIndex: ctx.HiddenRowIDIndex,
		HasTopLevelOrder: ctx.HasTopLevelOrder,
	}
}

func generateResultID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("res-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}

var (
	complexQueryRe = regexp.MustCompile(`(?is)\b(JOIN|UNION|INTERSECT|MINUS|EXCEPT|GROUP\s+BY|HAVING|CONNECT\s+BY|START\s+WITH)\b`)
	distinctRe     = regexp.MustCompile(`(?is)^\s*SELECT\s+(DISTINCT|UNIQUE)\b`)
	cteRe          = regexp.MustCompile(`(?is)^\s*WITH\b`)
	derivedFromRe  = regexp.MustCompile(`(?is)\bFROM\s*\(`)
)

// gridIdentifier 保留标识符的原始 token、解码后的名字以及是否显式加引号（DB-01）。
// Oracle 会把未加引号的标识符折叠成大写，因此 emp / "EMP" 是同一个名字，
// 而 "emp" 是区分大小写的另一个名字。丢失 quoted 标记会让注入的引用指向错误对象。
type gridIdentifier struct {
	Raw    string // 原始 SQL token，例如 emp / "emp" / `emp`
	Name   string // 去掉引号并还原转义后的名字
	Quoted bool   // 是否显式加引号
}

// parseGridIdentifierToken 解析单个标识符 token，保留引号语义。
func parseGridIdentifierToken(kind, token string) gridIdentifier {
	raw := strings.TrimSpace(token)
	id := gridIdentifier{Raw: raw}
	if len(raw) >= 2 {
		first, last := raw[0], raw[len(raw)-1]
		if (first == '"' && last == '"') || (first == '`' && last == '`') {
			id.Quoted = true
			id.Name = strings.ReplaceAll(raw[1:len(raw)-1], string(first)+string(first), string(first))
			return id
		}
	}
	id.Name = raw
	return id
}

func (id gridIdentifier) known() bool { return id.Name != "" }

// normalized 返回按方言规范化后的名字：Oracle 未加引号标识符统一大写，
// 显式加引号的名称保留精确大小写；其它方言保持原样。
func (id gridIdentifier) normalized(kind string) string {
	if id.Name == "" {
		return ""
	}
	if kind == KindOracle && !id.Quoted {
		return strings.ToUpper(id.Name)
	}
	return id.Name
}

// gridIdentifierEqual 比较两个标识符是否指向同一个名字。
// 规范化后的精确比较让 emp 与 "EMP" 相等、emp 与 "emp" 不等。
func gridIdentifierEqual(kind string, a, b gridIdentifier) bool {
	an, bn := a.normalized(kind), b.normalized(kind)
	if an == "" || bn == "" {
		return false
	}
	if an == bn {
		return true
	}
	return kind == KindMySQL && strings.EqualFold(an, bn)
}

// splitGridQualifiedToken 按引号外的第一个点拆分 schema.table 原始 token。
func splitGridQualifiedToken(name string) (schema, object string) {
	name = strings.TrimSpace(name)
	var quote byte
	for i := 0; i < len(name); i++ {
		c := name[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '`' {
			quote = c
			continue
		}
		if c == '.' {
			return strings.TrimSpace(name[:i]), strings.TrimSpace(name[i+1:])
		}
	}
	return "", name
}

// ParsedGridQuery 包含从 SQL 语法中提取的单表结构
type ParsedGridQuery struct {
	Schema      string
	Table       string
	TableAlias  string
	SchemaIdent gridIdentifier
	TableIdent  gridIdentifier
	AliasIdent  gridIdentifier
	// WildcardQualifier 是 alias.* 的前缀；裸 * 时为空
	WildcardQualifier gridIdentifier
	IsWildcard        bool
	Projections       []string
	HasTopOrder       bool
	WhereClause       string
}

// parseGridQuerySyntax 进行受限单表 SQL 语法提取
func parseGridQuerySyntax(kind, sql string) (*ParsedGridQuery, error) {
	trimmed := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), "; \t\r\n"))
	if trimmed == "" {
		return nil, errors.New("查询 SQL 不能为空")
	}
	clean := stripSQLComments(trimmed)
	upper := strings.ToUpper(clean)

	if !strings.HasPrefix(upper, "SELECT") {
		return nil, errors.New("仅支持 SELECT 查询")
	}
	if cteRe.MatchString(clean) {
		return nil, errors.New("CTE 查询无法确定目标基表")
	}
	if derivedFromRe.MatchString(clean) {
		return nil, errors.New("派生表或子查询无法确定目标基表")
	}
	if distinctRe.MatchString(clean) {
		return nil, errors.New("DISTINCT 查询不支持网格编辑")
	}
	if complexQueryRe.MatchString(clean) {
		return nil, errors.New("JOIN、聚合、层次或复合查询不支持网格编辑")
	}

	// 提取列投影
	items, err := extractSelectProjections(clean)
	if err != nil {
		return nil, err
	}

	// 提取 FROM 表与别名
	schemaIdent, tableIdent, aliasIdent, err := parseGridFromClause(kind, clean)
	if err != nil {
		return nil, err
	}

	isWildcard := len(items) == 1 && (items[0] == "*" || strings.HasSuffix(items[0], ".*"))
	wildcardQualifier := gridIdentifier{}
	if len(items) == 1 && strings.HasSuffix(items[0], ".*") {
		wildcardQualifier = parseGridIdentifierToken(kind, strings.TrimSpace(strings.TrimSuffix(items[0], ".*")))
	}

	return &ParsedGridQuery{
		Schema:            schemaIdent.normalized(kind),
		Table:             tableIdent.normalized(kind),
		TableAlias:        aliasIdent.normalized(kind),
		SchemaIdent:       schemaIdent,
		TableIdent:        tableIdent,
		AliasIdent:        aliasIdent,
		WildcardQualifier: wildcardQualifier,
		IsWildcard:        isWildcard,
		Projections:       items,
		HasTopOrder:       QueryHasTopLevelOrderBy(kind, clean),
	}, nil
}

// parseGridFromClause 提取 FROM 子句中的单一基表，排除逗号隐式连接
func parseGridFromClause(kind, noComments string) (schemaIdent, tableIdent, aliasIdent gridIdentifier, err error) {
	match := fromTableRe.FindStringSubmatch(noComments)
	if len(match) < 2 || strings.TrimSpace(match[1]) == "" {
		return schemaIdent, tableIdent, aliasIdent, errors.New("无法定位查询的 FROM 基表")
	}

	fullTarget := strings.TrimSpace(match[1])
	afterFromIdx := strings.Index(noComments, match[0]) + len(match[0])
	rest := noComments[afterFromIdx:]

	// 截取到下一个子句之前
	beforeNextClause := rest
	for _, clause := range []string{"WHERE", "ORDER", "GROUP", "LIMIT", "OFFSET", "FETCH", "FOR", ";"} {
		idx := strings.Index(strings.ToUpper(beforeNextClause), clause)
		if idx >= 0 {
			// 必须是独立单词边界
			if idx == 0 || isSpaceOrPunct(beforeNextClause[idx-1]) {
				end := idx + len(clause)
				if end >= len(beforeNextClause) || isSpaceOrPunct(beforeNextClause[end]) {
					beforeNextClause = beforeNextClause[:idx]
				}
			}
		}
	}

	// 检查是否有逗号分隔多表
	if strings.Contains(beforeNextClause, ",") {
		return schemaIdent, tableIdent, aliasIdent, errors.New("多表笛卡尔积或隐式连接不支持网格编辑")
	}

	// 提取表别名（如果有），保留原始 token 与 quoted 标记
	aliasWords := strings.Fields(strings.TrimSpace(beforeNextClause))
	if len(aliasWords) > 0 {
		if strings.EqualFold(aliasWords[0], "AS") {
			if len(aliasWords) > 1 {
				aliasIdent = parseGridIdentifierToken(kind, aliasWords[1])
			}
		} else if isIdent(aliasWords[0]) {
			aliasIdent = parseGridIdentifierToken(kind, aliasWords[0])
		}
	}

	// 拆分 schema.table，保留各自 token
	schemaToken, tableToken := splitGridQualifiedToken(fullTarget)
	schemaIdent = parseGridIdentifierToken(kind, schemaToken)
	tableIdent = parseGridIdentifierToken(kind, tableToken)
	if strings.EqualFold(tableIdent.Name, "DUAL") {
		return gridIdentifier{}, gridIdentifier{}, gridIdentifier{}, errors.New("DUAL 伪表不支持网格编辑")
	}

	return schemaIdent, tableIdent, aliasIdent, nil
}

// findTopLevelFromKeyword 扫描 SELECT 与外层 FROM 之间的投影分界点
func findTopLevelFromKeyword(clean string) int {
	inSingle := false
	inDouble := false
	depth := 0
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
				if i+1 < n && clean[i+1] == '"' {
					i++
				} else {
					inDouble = false
				}
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
				var next byte = ' '
				if i+4 < n {
					next = clean[i+4]
				}
				if isSpaceOrPunct(prev) && isSpaceOrPunct(next) {
					return i
				}
			}
		}
	}
	return -1
}

// oracleRowIDRewritePlan 是纯语法层面的 ROWID 改写决策结果（不访问数据库）
type oracleRowIDRewritePlan struct {
	SQL        string
	Schema     string
	Table      string
	TableAlias string
	Parsed     *ParsedGridQuery
}

// gridProjectionSourcePart 去掉投影项的 "AS 别名" 或尾随别名，返回来源表达式部分。
func gridProjectionSourcePart(item string) string {
	trimmed := strings.TrimSpace(item)
	inSingle, inDouble, inBacktick := false, false, false
	depth := 0
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
				if i+1 < len(trimmed) && trimmed[i+1] == '"' {
					i++
				} else {
					inDouble = false
				}
			}
			continue
		}
		if inBacktick {
			if c == '`' {
				inBacktick = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '`':
			inBacktick = true
		case '(':
			depth++
		case ')':
			depth--
		default:
			if depth == 0 && i+2 < len(trimmed) && (i == 0 || isSpaceOrPunct(trimmed[i-1])) &&
				strings.EqualFold(trimmed[i:i+2], "AS") && isSpaceOrPunct(trimmed[i+2]) {
				return strings.TrimSpace(trimmed[:i])
			}
		}
	}
	// 没有显式 AS：按顶层最后一个空格切分（与 parseProjectionItem 的规则一致）
	lastSpace := -1
	inSingle, inDouble, inBacktick, depth = false, false, false, 0
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
				if i+1 < len(trimmed) && trimmed[i+1] == '"' {
					i++
				} else {
					inDouble = false
				}
			}
			continue
		}
		if inBacktick {
			if c == '`' {
				inBacktick = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '`':
			inBacktick = true
		case '(':
			depth++
		case ')':
			depth--
		case ' ', '\t':
			if depth == 0 {
				lastSpace = i
			}
		}
	}
	if lastSpace >= 0 {
		return strings.TrimSpace(trimmed[:lastSpace])
	}
	return trimmed
}

// gridProjectionQualifier 校验投影项可证明来自单一物理列，并返回其表前缀（可能为空）。
func gridProjectionQualifier(kind, item string) (gridIdentifier, error) {
	if _, _, _, err := parseProjectionItem(kind, item); err != nil {
		return gridIdentifier{}, err
	}
	sourcePart := gridProjectionSourcePart(item)
	depth := 0
	quote := byte(0)
	lastDot := -1
	for i := 0; i < len(sourcePart); i++ {
		c := sourcePart[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '`' {
			quote = c
			continue
		}
		if c == '.' {
			if depth == 0 {
				lastDot = i
			}
			continue
		}
	}
	if lastDot < 0 {
		return gridIdentifier{}, nil
	}
	prefix := strings.TrimSpace(sourcePart[:lastDot])
	if prefix == "" {
		return gridIdentifier{}, fmt.Errorf("投影列 %q 格式非法", item)
	}
	if strings.Contains(prefix, ".") {
		return gridIdentifier{}, fmt.Errorf("投影列 %q 的多段前缀无法确认属于目标表，不能追加 ROWID 定位列", item)
	}
	return parseGridIdentifierToken(kind, prefix), nil
}

// gridRowIDProjectionAllowed 建立 ROWID 改写的严格允许清单（DB-02）：
// 只有单一真实表上的直接物理列投影，或可确认属于该表的 alias.* 通配符，
// 才允许追加逐行 ROWID 表达式。聚合、函数、表达式、常量、伪列、无法确认的来源
// 一律返回错误，由调用方保留原 SQL 并只关闭编辑能力。
func gridRowIDProjectionAllowed(kind string, parsed *ParsedGridQuery) error {
	if parsed == nil {
		return errors.New("无法解析查询来源")
	}
	if !parsed.TableIdent.known() {
		return errors.New("无法确定查询的单一基表")
	}
	qualifiers := []gridIdentifier{parsed.TableIdent}
	if parsed.AliasIdent.known() {
		qualifiers = append(qualifiers, parsed.AliasIdent)
	}
	matchesQualifier := func(candidate gridIdentifier) bool {
		for _, q := range qualifiers {
			if gridIdentifierEqual(kind, candidate, q) {
				return true
			}
		}
		return false
	}

	if parsed.IsWildcard {
		if !parsed.WildcardQualifier.known() {
			return nil // 裸 * 只有一个来源
		}
		if matchesQualifier(parsed.WildcardQualifier) {
			return nil
		}
		return fmt.Errorf("通配符前缀 %q 无法确认属于目标表 %s", parsed.WildcardQualifier.Raw, parsed.Table)
	}

	for _, item := range parsed.Projections {
		qualifier, err := gridProjectionQualifier(kind, item)
		if err != nil {
			return err
		}
		if qualifier.known() && !matchesQualifier(qualifier) {
			return fmt.Errorf("投影列 %q 的前缀无法确认属于目标表 %s", item, parsed.Table)
		}
	}
	return nil
}

// planOracleRowIDRewrite 只做语法与标识符校验，不访问数据库（DB-01/DB-02）。
func planOracleRowIDRewrite(kind, sqlText string) (*oracleRowIDRewritePlan, error) {
	trimmed := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sqlText), "; \t\r\n"))
	if trimmed == "" {
		return nil, errors.New("SQL 不能为空")
	}
	clean := stripSQLComments(trimmed)
	parsed, err := parseGridQuerySyntax(kind, clean)
	if err != nil {
		return nil, err
	}
	if err := gridRowIDProjectionAllowed(kind, parsed); err != nil {
		return nil, err
	}

	fromIdx := findTopLevelFromKeyword(clean)
	if fromIdx < 0 {
		return nil, errors.New("无法定位 FROM 关键字")
	}

	projStr := strings.TrimSpace(clean[6:fromIdx])
	qualifier := parsed.TableIdent
	if parsed.AliasIdent.known() {
		qualifier = parsed.AliasIdent
	}
	quotedQualifier, qErr := quoteGridIdentifier(kind, qualifier.normalized(kind), "表别名")
	if qErr != nil {
		return nil, qErr
	}

	var newProj string
	switch {
	case parsed.IsWildcard && strings.HasSuffix(projStr, ".*"):
		newProj = projStr + `, ROWIDTOCHAR(` + quotedQualifier + `.ROWID) AS "__KAIRO_EDIT_RID__"`
	case parsed.IsWildcard:
		newProj = quotedQualifier + `.*, ROWIDTOCHAR(` + quotedQualifier + `.ROWID) AS "__KAIRO_EDIT_RID__"`
	default:
		newProj = projStr + `, ROWIDTOCHAR(` + quotedQualifier + `.ROWID) AS "__KAIRO_EDIT_RID__"`
	}

	return &oracleRowIDRewritePlan{
		SQL:        "SELECT " + newProj + " " + strings.TrimSpace(clean[fromIdx:]),
		Schema:     parsed.Schema,
		Table:      parsed.Table,
		TableAlias: parsed.TableAlias,
		Parsed:     parsed,
	}, nil
}

// RewriteOracleQueryForHiddenRowID 为 Oracle 单表受限查询重写投影，增加 ROWIDTOCHAR 隐藏定位列。
// 该函数只做语法层面的允许清单校验；是否可以在真实库上改写还必须先确认目标表是普通堆表，
// 由 Manager.PlanOracleRowIDRewrite 负责（DB-02）。
func RewriteOracleQueryForHiddenRowID(sqlText string) (string, error) {
	trimmed := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sqlText), "; \t\r\n"))
	if trimmed == "" {
		return "", errors.New("SQL 不能为空")
	}
	if strings.Contains(trimmed, "__KAIRO_EDIT_RID__") {
		return trimmed, nil
	}
	plan, err := planOracleRowIDRewrite(KindOracle, sqlText)
	if err != nil {
		return sqlText, err
	}
	return plan.SQL, nil
}

// gridHeapFact 记录某个 Oracle 表是否为受支持的普通堆表
type gridHeapFact struct {
	IsHeap bool
}

func gridHeapTableCacheKey(sourceID, schema, table string) string {
	return fmt.Sprintf("%s\x00heap_table\x00%s\x00%s", sourceID, strings.ToUpper(schema), strings.ToUpper(table))
}

// SetCachedHeapTable 设置 Oracle 普通堆表缓存（用于测试或快速判定）
func (m *Manager) SetCachedHeapTable(sourceID, schema, table string, isHeap bool) {
	metadataCacheSet(m, gridHeapTableCacheKey(sourceID, schema, table), gridHeapFact{IsHeap: isHeap})
}

// isOracleHeapTable 返回 (是否普通堆表, 是否已确认)。
// 查询失败、被取消或未取回结果时返回未确认，调用方必须按“不可编辑”处理；
// 旧实现默认 isHeap=true 并吞掉查询错误，会把 IOT/视图/未知表当成可 ROWID 编辑（DB-02）。
func (m *Manager) isOracleHeapTable(ctx context.Context, source Source, schema, table string) (bool, bool) {
	if source.Kind != KindOracle {
		return false, true
	}
	if strings.TrimSpace(schema) == "" || strings.TrimSpace(table) == "" {
		return false, false
	}
	cacheKey := gridHeapTableCacheKey(source.ID, schema, table)
	if cached, ok := metadataCacheGet[gridHeapFact](m, cacheKey); ok {
		return cached.IsHeap, true
	}
	isHeap, known := false, false
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM all_tables WHERE (owner = :1 OR UPPER(owner) = UPPER(:1)) AND (table_name = :2 OR UPPER(table_name) = UPPER(:2)) AND temporary = 'N' AND iot_type IS NULL`, schema, table).Scan(&count); err != nil {
			return err
		}
		isHeap = count > 0
		known = true
		return nil
	})
	if err != nil || !known {
		return false, false
	}
	metadataCacheSet(m, cacheKey, gridHeapFact{IsHeap: isHeap})
	return isHeap, true
}

const (
	// gridMetadataPlanBudget 元数据规划自身的预算上限；实际取查询超时的 1/4 并夹在本区间内（DB-03）。
	gridMetadataPlanBudget = 1500 * time.Millisecond
	gridMetadataMinBudget  = 200 * time.Millisecond
)

// gridConcurrencyTokenKey 标记该 context 已经持有 Manager 的全局并发令牌。
// 元数据链路在持有令牌的请求内共享同一令牌，不再重复申请（DB-03）。
type gridConcurrencyTokenKey struct{}

func withGridConcurrencyToken(ctx context.Context) context.Context {
	return context.WithValue(ctx, gridConcurrencyTokenKey{}, true)
}

func gridConcurrencyTokenHeld(ctx context.Context) bool {
	held, _ := ctx.Value(gridConcurrencyTokenKey{}).(bool)
	return held
}

// gridMetadataBudgetContext 给辅助元数据规划一个独立短预算，
// 拿不到就降级为只读，而不是把辅助分析耗时算成查询首包的前置条件（DB-03）。
func gridMetadataBudgetContext(parent context.Context, source Source) (context.Context, context.CancelFunc) {
	budget := gridMetadataPlanBudget
	if timeout := source.Timeout(); timeout > 0 {
		if quarter := timeout / 4; quarter < budget {
			budget = quarter
		}
	}
	if budget < gridMetadataMinBudget {
		budget = gridMetadataMinBudget
	}
	return context.WithTimeout(parent, budget)
}

// gridMetadataSnapshot 是编辑能力分析所需的、与结果列无关的元数据快照。
// 注意：这里不缓存“是否普通堆表”，因为该事实可能在确认前不可用，
// 一旦把未知结果写进缓存就无法再用 SetCachedHeapTable/后续查询纠正（DB-02）。
type gridMetadataSnapshot struct {
	Fields  []Field
	Indexes []IndexInfo
}

func gridMetadataCacheKey(sourceID, fingerprint, schema, table string) string {
	return fmt.Sprintf("%s\x00gridmeta\x00%s\x00%s\x00%s", sourceID, fingerprint, strings.ToUpper(schema), strings.ToUpper(table))
}

// gridQueryPreparation 是在打开流式查询游标之前完成的受限规划结果（DB-01/DB-02/DB-03）。
// 它同时承载 ROWID 改写决策与编辑能力所需的元数据，避免游标打开后再发起嵌套池请求。
type gridQueryPreparation struct {
	Parsed    *ParsedGridQuery
	Schema    string
	Table     string
	Fields    []Field
	Indexes   []IndexInfo
	HeapKnown bool
	HeapTable bool
	RowIDSQL  string // 非空表示已确认可安全追加 ROWID 定位列的改写结果
	Reason    string // 非空表示编辑能力规划降级为只读的原因
}

// gridCachedFetch 先读元数据缓存，未命中时合并同一 key 的并发刷新（DB-03）。
// 等待刷新期间若预算耗尽则返回未命中，由调用方降级为只读。
func gridCachedFetch[T any](m *Manager, ctx context.Context, key string, load func(context.Context) (T, error)) (T, bool) {
	var zero T
	if m == nil {
		return zero, false
	}
	if cached, ok := metadataCacheGet[T](m, key); ok {
		return cached, true
	}
	for attempt := 0; attempt < 2; attempt++ {
		flight, leader := m.beginGridFetch(key)
		if !leader {
			select {
			case <-flight:
			case <-ctx.Done():
				return zero, false
			}
			if cached, ok := metadataCacheGet[T](m, key); ok {
				return cached, true
			}
			continue
		}
		value, err := load(ctx)
		m.endGridFetch(key, flight)
		if err != nil {
			return zero, false
		}
		metadataCacheSet(m, key, value)
		return value, true
	}
	return zero, false
}

// gridMetadataFor 取得（或刷新）某个 source fingerprint/schema/table 的元数据快照。
func (m *Manager) gridMetadataFor(ctx context.Context, source Source, schema, table string) (gridMetadataSnapshot, bool) {
	key := gridMetadataCacheKey(source.ID, sourceFingerprint(source), schema, table)
	return gridCachedFetch(m, ctx, key, func(ctx context.Context) (gridMetadataSnapshot, error) {
		fields, err := m.Fields(ctx, source, schema, table)
		if err != nil {
			return gridMetadataSnapshot{}, err
		}
		snapshot := gridMetadataSnapshot{Fields: fields}
		if indexes, idxErr := m.Indexes(ctx, source, schema, table); idxErr == nil {
			snapshot.Indexes = indexes
		}
		return snapshot, nil
	})
}

// prepareGridQuery 在占用流式查询连接之前完成与结果列无关的受限规划：
// 语法允许清单、ROWID 改写前置的普通堆表确认，以及编辑能力所需的元数据快照。
// 任何一步拿不到结果都只降级为只读，不影响原始查询的执行与读取能力（DB-02/DB-03）。
func (m *Manager) prepareGridQuery(ctx context.Context, source Source, sqlText string, needEditable bool) *gridQueryPreparation {
	if m == nil || source.Kind == KindRedis || source.Kind == "" {
		return nil
	}
	prep := &gridQueryPreparation{}
	parsed, err := parseGridQuerySyntax(source.Kind, sqlText)
	if err != nil {
		prep.Reason = err.Error()
		return prep
	}
	prep.Parsed = parsed
	schema := parsed.Schema
	if schema == "" {
		schema = ResolveSchema(source, "")
	}
	prep.Schema = schema
	prep.Table = parsed.Table
	if !parsed.TableIdent.known() {
		prep.Reason = "无法确定查询的单一基表"
		return prep
	}

	var rowIDPlan *oracleRowIDRewritePlan
	if source.Kind == KindOracle {
		if plan, planErr := planOracleRowIDRewrite(source.Kind, sqlText); planErr == nil {
			rowIDPlan = plan
		}
	}
	if rowIDPlan == nil && !needEditable {
		return prep
	}
	// 普通堆表事实是 ROWID 改写的前提，也是 oracle_rowid 编辑能力的前提（DB-02）。
	// 查询失败或预算不足时保持未知，调用方按只读处理。
	if source.Kind == KindOracle {
		heapCtx, cancelHeap := gridMetadataBudgetContext(ctx, source)
		isHeap, known := m.isOracleHeapTable(heapCtx, source, schema, parsed.Table)
		cancelHeap()
		if known {
			prep.HeapKnown, prep.HeapTable = true, isHeap
		}
		if rowIDPlan != nil && known && isHeap {
			prep.RowIDSQL = rowIDPlan.SQL
		}
	}
	if !needEditable {
		return prep
	}

	metaCtx, cancelMeta := gridMetadataBudgetContext(ctx, source)
	snapshot, ok := m.gridMetadataFor(metaCtx, source, schema, parsed.Table)
	cancelMeta()
	if !ok {
		if prep.Reason == "" {
			prep.Reason = fmt.Sprintf("无法在受限预算内读取目标基表 %s.%s 的元数据，已降级为只读", schema, parsed.Table)
		}
		return prep
	}
	prep.Fields = snapshot.Fields
	prep.Indexes = snapshot.Indexes
	if len(prep.Fields) == 0 && prep.Reason == "" {
		prep.Reason = fmt.Sprintf("无法读取目标基表 %s.%s 的元数据", schema, parsed.Table)
	}
	return prep
}

// PlanOracleRowIDRewrite 在打开游标之前决定能否安全追加 ROWID 定位列（DB-01/DB-02）：
// 只有语法允许且已确认目标表是受支持的普通堆表时才返回改写后的 SQL。
func (m *Manager) PlanOracleRowIDRewrite(ctx context.Context, source Source, sqlText string) (string, bool) {
	prep := m.prepareGridQuery(ctx, source, sqlText, false)
	if prep == nil || prep.RowIDSQL == "" {
		return sqlText, false
	}
	return prep.RowIDSQL, true
}

// AnalyzeGridQuery 分析查询并建立完整的 ResultEditContext。
// 元数据获取发生在这里，纯分析由 AnalyzeGridQueryWithMetadata 完成（DB-03）。
func (m *Manager) AnalyzeGridQuery(ctx context.Context, source Source, sessionID, executedSQL string, resultColumns []Column, hiddenRowID ...int) (*ResultEditContext, error) {
	hiddenRowIDIdx := -1
	if len(hiddenRowID) > 0 {
		hiddenRowIDIdx = hiddenRowID[0]
	}
	prep := m.prepareGridQuery(ctx, source, executedSQL, true)
	return AnalyzeGridQueryWithMetadata(source, sessionID, executedSQL, resultColumns, prep, hiddenRowIDIdx), nil
}

// AnalyzeGridQueryWithMetadata 是纯分析函数：不申请连接、不访问数据库，
// 只根据已准备好的元数据快照绑定结果列并判定编辑能力。
func AnalyzeGridQueryWithMetadata(source Source, sessionID, executedSQL string, resultColumns []Column, prep *gridQueryPreparation, hiddenRowIDIdx int) *ResultEditContext {
	if hiddenRowIDIdx < 0 {
		hiddenRowIDIdx = -1
	}
	plan := &ResultEditContext{
		ResultID:          generateResultID(),
		SessionID:         sessionID,
		SourceID:          source.ID,
		SourceFingerprint: sourceFingerprint(source),
		Dialect:           source.Kind,
		ExecutedSQL:       executedSQL,
		IdentityPolicy:    "none",
		HiddenRowIDIndex:  hiddenRowIDIdx,
		CreatedAt:         time.Now(),
	}
	if prep == nil {
		plan.Reason = "未完成查询元数据规划，编辑能力已关闭"
		return plan
	}
	if prep.Parsed == nil {
		plan.Schema, plan.Table = prep.Schema, prep.Table
		plan.Reason = prep.Reason
		if plan.Reason == "" {
			plan.Reason = "无法解析查询来源"
		}
		return plan
	}
	parsed := prep.Parsed
	plan.HasTopLevelOrder = parsed.HasTopOrder
	plan.TableAlias = parsed.TableAlias
	plan.Schema = prep.Schema
	plan.Table = prep.Table

	fields := prep.Fields
	if len(fields) == 0 {
		plan.Reason = prep.Reason
		if plan.Reason == "" {
			plan.Reason = fmt.Sprintf("无法读取目标基表 %s.%s 的元数据", plan.Schema, plan.Table)
		}
		return plan
	}

	fieldMap := make(map[string]Field, len(fields))
	var pkCols []string
	for _, f := range fields {
		fieldMap[strings.ToUpper(f.Name)] = f
		if f.PrimaryKey {
			pkCols = append(pkCols, f.Name)
		}
	}
	plan.PrimaryKeys = pkCols

	// 读取唯一键约束（Phase 1 备用）
	var uniqueCols []string
	indexes := prep.Indexes
	for _, idx := range indexes {
		if strings.EqualFold(idx.Uniqueness, "UNIQUE") && len(idx.Columns) > 0 {
			allNonNullable := true
			for _, col := range idx.Columns {
				if f, ok := fieldMap[strings.ToUpper(col)]; ok && f.Nullable {
					allNonNullable = false
					break
				}
			}
			if allNonNullable {
				uniqueCols = idx.Columns
				break
			}
		}
	}
	plan.UniqueKeys = uniqueCols

	// 绑定结果列与物理基表列
	bindings := make([]GridColumnBinding, len(resultColumns))
	allProjectedPhysCols := make(map[string]int)

	if parsed.IsWildcard {
		// SELECT * 模式
		for i, col := range resultColumns {
			phys, exists := fieldMap[strings.ToUpper(col.Name)]
			binding := GridColumnBinding{
				Index:        i,
				ResultName:   col.Name,
				PhysicalName: col.Name,
				DataType:     col.Database,
				IsNullable:   col.Nullable,
			}
			if exists {
				binding.PhysicalName = phys.Name
				binding.IsPrimaryKey = phys.PrimaryKey
				binding.IsNullable = phys.Nullable
				if isUnsupportedLOBType(phys.DataType) {
					binding.Writable = false
					binding.ReadOnlyReason = "LOB/长文本字段不支持直接网格内嵌编辑"
				} else {
					binding.Writable = true
				}
				allProjectedPhysCols[strings.ToUpper(phys.Name)] = i
			} else {
				binding.Writable = false
				binding.ReadOnlyReason = "非基表列"
			}
			bindings[i] = binding
		}
	} else {
		// 显式投影列匹配
		for i, item := range parsed.Projections {
			if i >= len(resultColumns) {
				break
			}
			resCol := resultColumns[i]
			physName, _, _, parseErr := parseProjectionItem(source.Kind, item)
			binding := GridColumnBinding{
				Index:      i,
				ResultName: resCol.Name,
				DataType:   resCol.Database,
				IsNullable: resCol.Nullable,
			}
			if parseErr != nil {
				binding.Writable = false
				binding.ReadOnlyReason = parseErr.Error()
			} else {
				phys, exists := fieldMap[strings.ToUpper(physName)]
				if exists {
					binding.PhysicalName = phys.Name
					binding.IsPrimaryKey = phys.PrimaryKey
					binding.IsNullable = phys.Nullable
					if isUnsupportedLOBType(phys.DataType) {
						binding.Writable = false
						binding.ReadOnlyReason = "LOB/长文本字段不支持直接网格内嵌编辑"
					} else {
						binding.Writable = true
					}
					allProjectedPhysCols[strings.ToUpper(phys.Name)] = i
				} else {
					binding.Writable = false
					binding.ReadOnlyReason = "不是目标基表物理列"
				}
			}
			bindings[i] = binding
		}
	}
	plan.Columns = bindings

	// 判断身份策略 (Identity Policy)
	if len(pkCols) > 0 {
		hasAllPKs := true
		for _, pk := range pkCols {
			if _, ok := allProjectedPhysCols[strings.ToUpper(pk)]; !ok {
				hasAllPKs = false
				break
			}
		}
		if hasAllPKs {
			plan.IdentityPolicy = "pk"
			plan.CanUpdate = true
			plan.CanDelete = true
		} else {
			plan.IdentityPolicy = "none"
			plan.CanUpdate = false
			plan.CanDelete = false
			plan.Reason = "当前结果缺少目标表的完整主键，无法可靠定位单行"
		}
	} else if len(uniqueCols) > 0 {
		// Phase 1: 经过验证的完整非空唯一键
		hasAllUniq := true
		for _, u := range uniqueCols {
			if _, ok := allProjectedPhysCols[strings.ToUpper(u)]; !ok {
				hasAllUniq = false
				break
			}
		}
		if hasAllUniq {
			plan.IdentityPolicy = "unique"
			plan.CanUpdate = true
			plan.CanDelete = true
		} else {
			plan.IdentityPolicy = "none"
			plan.CanUpdate = false
			plan.CanDelete = false
			plan.Reason = "目标表无主键且当前结果未包含完整非空唯一键"
		}
	} else {
		plan.IdentityPolicy = "none"
		plan.CanUpdate = false
		plan.CanDelete = false
		plan.Reason = "目标表未定义主键或受支持的唯一行标识"
	}

	// Phase 1: 若未满足完整 PK/Unique 键，但已确认是受支持的 Oracle 普通堆表且已取得隐藏 ROWID。
	// 堆表事实必须在改写前确认；未知时保持只读（DB-02）。
	if plan.IdentityPolicy == "none" && source.Kind == KindOracle && hiddenRowIDIdx >= 0 {
		switch {
		case !prep.HeapKnown:
			plan.Reason = "无法确认目标表是否为受支持的 Oracle 普通堆表，无主键时不开放 ROWID 编辑"
		case prep.HeapTable:
			plan.IdentityPolicy = "oracle_rowid"
			plan.HiddenRowIDIndex = hiddenRowIDIdx
			plan.CanUpdate = true
			plan.CanDelete = true
			plan.Reason = ""
		default:
			plan.Reason = "目标表非受支持 Oracle 普通堆表（如 IOT/临时表/视图），无主键时不支持 ROWID 编辑"
		}
	}

	// 只要单表物理来源明确，插入无需依赖已有行身份
	if source.MutationAllowed() && plan.Table != "" {
		plan.CanInsert = true
	}

	return plan
}

func isUnsupportedLOBType(dataType string) bool {
	upper := strings.ToUpper(dataType)
	return strings.Contains(upper, "BLOB") ||
		strings.Contains(upper, "CLOB") ||
		strings.Contains(upper, "LONG") ||
		strings.Contains(upper, "XML") ||
		strings.Contains(upper, "JSON") ||
		strings.Contains(upper, "GEOMETRY")
}

const maxRegisteredResultContexts = 1000

// RegisterResultContext 将生成的编辑上下文存入注册表
func (m *Manager) RegisterResultContext(plan *ResultEditContext) {
	if m == nil || plan == nil || plan.ResultID == "" {
		return
	}
	m.resultContextMu.Lock()
	defer m.resultContextMu.Unlock()
	if len(m.resultContexts) >= maxRegisteredResultContexts {
		// 淘汰最早的一批上下文 (LRU / FIFO)
		now := time.Now()
		for id, item := range m.resultContexts {
			if now.Sub(item.CreatedAt) > 1*time.Hour || len(m.resultContexts) >= maxRegisteredResultContexts {
				delete(m.resultContexts, id)
			}
		}
	}
	m.resultContexts[plan.ResultID] = plan
}

// GetResultContext 获取结果编辑上下文
func (m *Manager) GetResultContext(resultID string) (*ResultEditContext, bool) {
	if m == nil || resultID == "" {
		return nil, false
	}
	m.resultContextMu.RLock()
	defer m.resultContextMu.RUnlock()
	plan, ok := m.resultContexts[resultID]
	if !ok {
		return nil, false
	}
	return plan, true
}

// InvalidateResultContextsForSource 当数据源配置变更时作废该数据源的全部历史结果
func (m *Manager) InvalidateResultContextsForSource(sourceID string) {
	if m == nil || sourceID == "" {
		return
	}
	m.resultContextMu.Lock()
	defer m.resultContextMu.Unlock()
	for id, item := range m.resultContexts {
		if item.SourceID == sourceID {
			delete(m.resultContexts, id)
		}
	}
}
