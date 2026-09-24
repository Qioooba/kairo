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
	// TableFields 服务端元数据确认的目标基表字段（含未投影列的声明类型）。
	// INSERT 的参数类型绑定依据，不下发给前端（DB-04）。
	TableFields []Field `json:"table_fields,omitempty"`
}

// GridEditPlanColumnSummary 是按结果索引关联的只读列绑定摘要（DBUI-01）。
// 前端据此决定某一格能否编辑并显示原因；是否可写与物理列映射仍由服务器最终校验。
type GridEditPlanColumnSummary struct {
	Index          int    `json:"index"`
	ResultName     string `json:"result_name"`
	PhysicalName   string `json:"physical_name,omitempty"`
	Writable       bool   `json:"writable"`
	ReadOnlyReason string `json:"read_only_reason,omitempty"`
}

// GridEditPlanSummary 序列化返回给前端的只读能力摘要
type GridEditPlanSummary struct {
	ResultID         string                      `json:"result_id"`
	Schema           string                      `json:"schema"`
	Table            string                      `json:"table"`
	IdentityPolicy   string                      `json:"identity_policy"` // "pk", "oracle_rowid", "unique", "none"
	CanInsert        bool                        `json:"can_insert"`
	CanUpdate        bool                        `json:"can_update"`
	CanDelete        bool                        `json:"can_delete"`
	Reason           string                      `json:"reason,omitempty"`
	PrimaryKeys      []string                    `json:"primary_keys,omitempty"`
	UniqueKeys       []string                    `json:"unique_keys,omitempty"`
	HiddenRowIDIndex int                         `json:"hidden_rowid_index,omitempty"`
	HasTopLevelOrder bool                        `json:"has_top_level_order"`
	Columns          []GridEditPlanColumnSummary `json:"columns"`
}

func (ctx *ResultEditContext) ToSummary() *GridEditPlanSummary {
	if ctx == nil {
		return nil
	}
	columns := make([]GridEditPlanColumnSummary, 0, len(ctx.Columns))
	for _, col := range ctx.Columns {
		columns = append(columns, GridEditPlanColumnSummary{
			Index:          col.Index,
			ResultName:     col.ResultName,
			PhysicalName:   col.PhysicalName,
			Writable:       col.Writable,
			ReadOnlyReason: col.ReadOnlyReason,
		})
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
		Columns:          columns,
	}
}

func generateResultID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("res-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}

var (
	// STRAIGHT_JOIN 是 MySQL 特有的连接写法，字面不含 JOIN，必须单独列出（DB-08）。
	complexQueryRe = regexp.MustCompile(`(?is)\b(JOIN|STRAIGHT_JOIN|UNION|INTERSECT|MINUS|EXCEPT|GROUP\s+BY|HAVING|CONNECT\s+BY|START\s+WITH)\b`)
	distinctRe     = regexp.MustCompile(`(?is)^\s*SELECT\s+(DISTINCT|UNIQUE)\b`)
	cteRe          = regexp.MustCompile(`(?is)^\s*WITH\b`)
	derivedFromRe  = regexp.MustCompile(`(?is)\bFROM\s*\(`)
	// gridFromHeadJoinRe 兜住"FROM 头部里出现了连接关键字"的所有写法。
	// 只要 FROM 与下一个顶层子句之间出现这些 token，就说明它不是单一基表；
	// 拿不准时一律拒绝编辑（fail-closed），绝不把多表结果当成单表来写。
	gridFromHeadJoinRe = regexp.MustCompile(`(?is)\b(NATURAL|INNER|OUTER|CROSS|LEFT|RIGHT|FULL|JOIN|STRAIGHT_JOIN|USING|ON|APPLY|LATERAL|PIVOT|UNPIVOT)\b`)
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
	clean := stripSQLComments(kind, trimmed)
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
	items, err := extractSelectProjections(kind, clean)
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

// parseGridFromClause 提取**最外层** FROM 子句中的单一基表，排除逗号隐式连接。
//
// P1 修复：`SELECT ID, (SELECT COUNT(*) FROM ARCHIVE) AS N FROM LIVE` 这类投影里带标量子查询的
// 查询，旧实现用整条 SQL 的第一个 `FROM` 正则匹配，会把 ARCHIVE 当成编辑目标表，
// 于是用户在 LIVE 结果网格里的修改会被构造成对 ARCHIVE 的 UPDATE。这里改为从
// 最外层 FROM 关键字（跳过括号、字符串与三种引号标识符）之后开始解析。
func parseGridFromClause(kind, noComments string) (schemaIdent, tableIdent, aliasIdent gridIdentifier, err error) {
	fromIdx := findTopLevelFromKeyword(noComments)
	if fromIdx < 0 {
		return schemaIdent, tableIdent, aliasIdent, errors.New("无法定位查询的 FROM 基表")
	}
	rest := noComments[fromIdx+len("FROM"):]

	// 截取到下一个顶层子句之前（括号/引号内的关键字不算边界）
	beforeNextClause := gridFromHeadBeforeNextClause(rest)
	if strings.TrimSpace(beforeNextClause) == "" {
		return schemaIdent, tableIdent, aliasIdent, errors.New("无法定位查询的 FROM 基表")
	}

	// 检查是否有逗号分隔多表
	if strings.Contains(beforeNextClause, ",") {
		return schemaIdent, tableIdent, aliasIdent, errors.New("多表笛卡尔积或隐式连接不支持网格编辑")
	}
	// DB-08: 兜住不含 JOIN 字样的连接写法（MySQL STRAIGHT_JOIN、以及任何落在 FROM 头部
	// 的 ON/USING/LEFT/CROSS/PIVOT 等）。这些 token 在单基表 FROM 里不可能出现。
	if gridFromHeadJoinRe.MatchString(beforeNextClause) {
		return schemaIdent, tableIdent, aliasIdent, errors.New("多表连接查询不支持网格编辑")
	}

	// 读取限定表名 token（schema.table），尾部用于解析别名
	fullTarget, aliasTail := splitGridLeadingQualifiedToken(beforeNextClause)
	fullTarget = strings.TrimSpace(fullTarget)
	if fullTarget == "" {
		return schemaIdent, tableIdent, aliasIdent, errors.New("无法定位查询的 FROM 基表")
	}

	// 提取表别名（如果有），保留原始 token 与 quoted 标记
	aliasWords := strings.Fields(strings.TrimSpace(aliasTail))
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

// gridHasWildcardProjection 判断投影列表里是否存在 `*` / `alias.*`。
// 只有单一通配符投影时 parsed.IsWildcard 为真，这里命中即代表通配符与显式列混用。
func gridHasWildcardProjection(items []string) bool {
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "*" || strings.HasSuffix(trimmed, ".*") {
			return true
		}
	}
	return false
}

// gridDisableEditing 整体关闭网格编辑能力，并把原因写到每一列上。
func gridDisableEditing(plan *ResultEditContext, reason string) {
	if plan == nil {
		return
	}
	plan.Reason = reason
	plan.CanInsert, plan.CanUpdate, plan.CanDelete = false, false, false
	for i := range plan.Columns {
		plan.Columns[i].Writable = false
		plan.Columns[i].ReadOnlyReason = reason
	}
}

// gridFromClauseKeywords 是 FROM 之后、表引用结束处的顶层子句关键字。
var gridFromClauseKeywords = []string{"WHERE", "ORDER", "GROUP", "LIMIT", "OFFSET", "FETCH", "FOR"}

// gridFromHeadBeforeNextClause 返回 FROM 之后到下一个顶层子句关键字（或 ';'）之间的文本。
// 括号、单引号字符串与双引号/反引号标识符内部的关键字不算边界。
func gridFromHeadBeforeNextClause(rest string) string {
	inSingle, inDouble, inBacktick := false, false, false
	depth := 0
	n := len(rest)

	for i := 0; i < n; i++ {
		c := rest[i]
		if inSingle {
			if c == '\'' {
				if i+1 < n && rest[i+1] == '\'' {
					i++
				} else {
					inSingle = false
				}
			}
			continue
		}
		if inDouble {
			if c == '"' {
				if i+1 < n && rest[i+1] == '"' {
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
			continue
		case '"':
			inDouble = true
			continue
		case '`':
			inBacktick = true
			continue
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		case ';':
			if depth == 0 {
				return rest[:i]
			}
			continue
		}
		if depth != 0 {
			continue
		}
		for _, kw := range gridFromClauseKeywords {
			if i+len(kw) > n || !strings.EqualFold(rest[i:i+len(kw)], kw) {
				continue
			}
			prevOK := i == 0 || isSpaceOrPunct(rest[i-1])
			next := i + len(kw)
			nextOK := next >= n || isSpaceOrPunct(rest[next])
			if prevOK && nextOK {
				return rest[:i]
			}
		}
	}
	return rest
}

// splitGridLeadingQualifiedToken 从 FROM 头部读取首个（可带 schema 前缀的）标识符 token，
// 返回原始 token 与剩余文本。引号内的点/空格不会被当作分隔符。
func splitGridLeadingQualifiedToken(s string) (token, tail string) {
	s = strings.TrimLeft(s, " \t\r\n")
	if s == "" {
		return "", ""
	}
	first, n1 := gridReadIdentSegment(s)
	if n1 == 0 {
		return "", s
	}
	// 可选 . 第二段（允许点两侧空白）
	j := n1
	for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r' || s[j] == '\n') {
		j++
	}
	if j < len(s) && s[j] == '.' {
		k := j + 1
		for k < len(s) && (s[k] == ' ' || s[k] == '\t' || s[k] == '\r' || s[k] == '\n') {
			k++
		}
		if second, n2 := gridReadIdentSegment(s[k:]); n2 > 0 {
			return s[:n1] + s[j:k] + second, s[k+n2:]
		}
	}
	return first, s[n1:]
}

// gridReadIdentSegment 读取一个标识符片段（带引号或裸标识符），返回片段与消耗字节数。
func gridReadIdentSegment(s string) (string, int) {
	if s == "" {
		return "", 0
	}
	if s[0] == '"' || s[0] == '`' {
		q := s[0]
		for i := 1; i < len(s); i++ {
			if s[i] != q {
				continue
			}
			if i+1 < len(s) && s[i+1] == q {
				i++
				continue
			}
			return s[:i+1], i + 1
		}
		return "", 0
	}
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '$' || c == '#' {
			i++
			continue
		}
		break
	}
	if i == 0 {
		return "", 0
	}
	return s[:i], i
}

// findTopLevelFromKeyword 扫描 SELECT 与外层 FROM 之间的投影分界点。
// 字符串常量、双引号/反引号标识符内部以及括号（含标量子查询）内部的 FROM 都不算最外层。
func findTopLevelFromKeyword(clean string) int {
	inSingle := false
	inDouble := false
	inBacktick := false
	depth := 0
	n := len(clean)

	for i := 6; i < n; i++ {
		c := clean[i]
		if inBacktick {
			if c == '`' {
				inBacktick = false
			}
			continue
		}
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
	clean := stripSQLComments(kind, trimmed)
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

// SetCachedBaseTable 设置“目标对象是否为真实基表”的缓存（用于测试或快速判定）。
// 视图/同义词为 false：它们没有可以直接 INSERT 的物理行（DB-08）。
func (m *Manager) SetCachedBaseTable(source Source, schema, table string, isBase bool) {
	metadataCacheSet(m, gridBaseTableCacheKey(source.ID, sourceFingerprint(source), schema, table), gridBaseTableFact{IsBaseTable: isBase})
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
	// DB-08: 失败/超时写短 TTL 负缓存，避免同一张表的字典查询在每次查询上重复等满预算。
	// 注意这是与 gridHeapFact 不同的类型：它只表示"这一小段时间内不要再试"，
	// 不会把"未知"固化成"不是堆表"，因此 DB-02 的语义（未知必须按只读处理、
	// 且随后仍可被 SetCachedHeapTable/成功查询纠正）保持不变。
	if _, bad := metadataCacheGet[gridMetadataUnavailable](m, cacheKey); bad {
		return false, false
	}
	isHeap, known := false, false
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM all_tables WHERE (owner = :1 OR UPPER(owner) = UPPER(:2)) AND (table_name = :3 OR UPPER(table_name) = UPPER(:4)) AND temporary = 'N' AND iot_type IS NULL`, schema, strings.ToUpper(schema), table, strings.ToUpper(table)).Scan(&count); err != nil {
			return err
		}
		isHeap = count > 0
		known = true
		return nil
	})
	if err != nil || !known {
		metadataCacheSetTTL(m, cacheKey, gridMetadataUnavailable{Reason: "普通堆表事实确认失败"}, gridMetadataUnavailableTTL)
		return false, false
	}
	metadataCacheSet(m, cacheKey, gridHeapFact{IsHeap: isHeap})
	return isHeap, true
}

const (
	// gridMetadataPlanBudget 元数据规划的总预算上限；实际取查询超时的 1/4 并夹在本区间内。
	//
	// DB-08: 实测（真实 Oracle 21c XE, KAIRO_LAB）单个字典查询的冷启动开销可达 ~1.9s
	// （`Indexes` 首次执行时的硬解析 + 数据字典填充），稳态则只有 ~10ms。
	// 旧的单段 1500ms 上限会把这类"本来能成功"的读取判成超时，然后静默把网格降级为只读
	// （实测视图场景：耗时 2.37s 且返回只读）。因此单段上限必须高于真实冷启动开销。
	// 取 3000ms 是刻意与旧实现的"两段各 1500ms"最坏值对齐：改成单预算后，
	// 最坏等待不会比过去更久，但一次成功的冷读取不会再被丢掉。
	gridMetadataPlanBudget = 3000 * time.Millisecond
	gridMetadataMinBudget  = 800 * time.Millisecond
	// gridMetadataUnavailableTTL 是元数据"短期不可用"负缓存的存活时间。
	// 只用于压制同一个对象的重复无效等待，不做长期记忆。
	gridMetadataUnavailableTTL = 15 * time.Second
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

// gridMetadataBudgetContext 给一次查询里的**全部**元数据规划工作开一个总预算。
//
// DB-08: 旧实现给"普通堆表确认"和"字段/索引"各开一个串行独立预算，最坏叠加到 ~3s，
// 每个查询各自重新计时也无法被缓存吸收。现在改成一次性总预算：
// 一次查询最多为元数据等待一个上限，拿不到就降级为只读，而不是把辅助分析的耗时
// 反复算进查询首包。
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

// gridMetadataUnavailable 是"元数据短期不可用"的负缓存标记（DB-08）。
//
// 只压制重复的无效等待：某张表的字典查询一旦超时，旧实现会在后续每次查询上
// 重新等满一次预算（实测表现为"连续执行也一直慢"）。写入短 TTL 后，同一个对象
// 在窗口内直接按只读处理，不再重复付出等待。注意它绝不代表"确认不可编辑"：
// 命中负缓存与读取失败的下场一致，都是保持只读。
type gridMetadataUnavailable struct {
	Reason string
}

func gridFieldsCacheKey(sourceID, fingerprint, schema, table string) string {
	return fmt.Sprintf("%s\x00gridfields\x00%s\x00%s\x00%s", sourceID, fingerprint, strings.ToUpper(schema), strings.ToUpper(table))
}

func gridIndexesCacheKey(sourceID, fingerprint, schema, table string) string {
	return fmt.Sprintf("%s\x00gridindexes\x00%s\x00%s\x00%s", sourceID, fingerprint, strings.ToUpper(schema), strings.ToUpper(table))
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
	// BaseKnown/BaseTable 记录目标对象是否为真实基表（DB-08）。
	// 视图/同义词没有行身份也没有可插入的物理目标；插入能力必须据此关闭。
	BaseKnown bool
	BaseTable bool
	RowIDSQL  string // 非空表示已确认可安全追加 ROWID 定位列的改写结果
	Reason    string // 非空表示编辑能力规划降级为只读的原因
}

// gridCachedFetch 先读元数据缓存，未命中时合并同一 key 的并发刷新（DB-03）。
// 等待刷新期间若预算耗尽则返回未命中，由调用方降级为只读。
//
// DB-08: 失败会写入短 TTL 负缓存，避免同一个慢/坏对象让每次查询都重新等满预算。
func gridCachedFetch[T any](m *Manager, ctx context.Context, key string, load func(context.Context) (T, error)) (T, bool) {
	var zero T
	if m == nil {
		return zero, false
	}
	if cached, ok := metadataCacheGet[T](m, key); ok {
		return cached, true
	}
	if bad, ok := metadataCacheGet[gridMetadataUnavailable](m, key); ok {
		_ = bad
		return zero, false
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
			metadataCacheSetTTL(m, key, gridMetadataUnavailable{Reason: err.Error()}, gridMetadataUnavailableTTL)
			return zero, false
		}
		metadataCacheSet(m, key, value)
		return value, true
	}
	return zero, false
}

// gridFieldsFor 取得（或刷新）目标对象的字段元数据。
// 字段查询很便宜（实测冷 ~10ms、稳态 ~0ms），因此总是先取：它是"是否需要 ROWID"的前提。
func (m *Manager) gridFieldsFor(ctx context.Context, source Source, schema, table string) ([]Field, bool) {
	key := gridFieldsCacheKey(source.ID, sourceFingerprint(source), schema, table)
	return gridCachedFetch(m, ctx, key, func(ctx context.Context) ([]Field, error) {
		return m.Fields(ctx, source, schema, table)
	})
}

// gridIndexesFor 取得（或刷新）目标对象的索引元数据。
//
// DB-08: 索引是整段元数据里最贵的一次字典查询（实测冷启动 ~1.9s），
// 而它只服务于"主键缺失时的唯一键定位"判定，因此只在主键不完整时才取。
func (m *Manager) gridIndexesFor(ctx context.Context, source Source, schema, table string) ([]IndexInfo, bool) {
	key := gridIndexesCacheKey(source.ID, sourceFingerprint(source), schema, table)
	return gridCachedFetch(m, ctx, key, func(ctx context.Context) ([]IndexInfo, error) {
		return m.Indexes(ctx, source, schema, table)
	})
}

// gridBareProjectionColumn 从单个投影项里取出"裸列名"；不是裸列（表达式、函数、
// 带别名、通配符）时返回空串。
//
// 只服务于执行前的保守判断，因此宁可不认：拿不准就当作"没有投影该列"，
// 于是回落到原有的 ROWID 路径，不会因为误判而少一条定位手段。
func gridBareProjectionColumn(kind, item string) string {
	trimmed := strings.TrimSpace(item)
	if trimmed == "" || trimmed == "*" || strings.HasSuffix(trimmed, ".*") {
		return ""
	}
	// 表达式 / 函数调用
	if strings.ContainsAny(trimmed, "()") {
		return ""
	}
	// 带别名或多余 token（`id AS x` / `id x`）一律不认，避免把别名当物理列
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return ""
	}
	_, object := splitGridQualifiedToken(trimmed)
	return parseGridIdentifierToken(kind, strings.TrimSpace(object)).Name
}

// gridBaseTableFact 记录某个对象是否为真实基表（视图/同义词为 false）。
type gridBaseTableFact struct {
	IsBaseTable bool
}

func gridBaseTableCacheKey(sourceID, fingerprint, schema, table string) string {
	return fmt.Sprintf("%s\x00basetable\x00%s\x00%s\x00%s", sourceID, fingerprint, strings.ToUpper(schema), strings.ToUpper(table))
}

// isBaseTable 返回 (是否真实基表, 是否已确认)。
//
// DB-08: 插入能力与"目标是不是真实表"直接相关。关联视图、同义词、派生来源都不存在
// 可以直接 INSERT 的物理行 —— 真实 Oracle 实测暴露过：`SELECT * FROM
// KAIRO_LAB.EMPLOYEE_DIRECTORY`（一个 JOIN 视图）在没有主键/唯一键时会被判为"不可更新"
// 却仍然开放 can_insert，用户点新增行只会在提交时撞上 ORA-01733。
//
// 对 Oracle 用 all_tables 判定（视图不在其中；IOT/临时表仍然算表），
// 对 MySQL 用 information_schema.tables 的 table_type。
func (m *Manager) isBaseTable(ctx context.Context, source Source, schema, table string) (bool, bool) {
	if source.Kind != KindOracle && source.Kind != KindMySQL {
		return false, false
	}
	if strings.TrimSpace(table) == "" {
		return false, false
	}
	cacheKey := gridBaseTableCacheKey(source.ID, sourceFingerprint(source), schema, table)
	if cached, ok := metadataCacheGet[gridBaseTableFact](m, cacheKey); ok {
		return cached.IsBaseTable, true
	}
	if _, bad := metadataCacheGet[gridMetadataUnavailable](m, cacheKey); bad {
		return false, false
	}
	isBase, known := false, false
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var count int
		var queryErr error
		if source.Kind == KindOracle {
			queryErr = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM all_tables WHERE (owner = :1 OR UPPER(owner) = UPPER(:2)) AND (table_name = :3 OR UPPER(table_name) = UPPER(:4))`, schema, strings.ToUpper(schema), table, strings.ToUpper(table)).Scan(&count)
		} else {
			queryErr = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE (table_schema = ? OR LOWER(table_schema) = LOWER(?)) AND (table_name = ? OR LOWER(table_name) = LOWER(?)) AND table_type = 'BASE TABLE'`, schema, schema, table, table).Scan(&count)
		}
		if queryErr != nil {
			return queryErr
		}
		isBase = count > 0
		known = true
		return nil
	})
	if err != nil || !known {
		metadataCacheSetTTL(m, cacheKey, gridMetadataUnavailable{Reason: "基表事实确认失败"}, gridMetadataUnavailableTTL)
		return false, false
	}
	metadataCacheSet(m, cacheKey, gridBaseTableFact{IsBaseTable: isBase})
	return isBase, true
}

// gridProjectionCoversPrimaryKey 判断投影是否**确定**已经覆盖目标表的全部主键列。
//
// 该判断只用于一个前置决策：是否还需要为这次查询追加 ROWID 定位列。
// 因为没有主键结果就退化为 ROWID 编辑，所以这里的判断必须保守：
//   - 投影是 * / alias.* 时，结果就是整表所有列，主键必然在内；
//   - 否则要求每个主键列都以裸标识符（id / e.id / "ID"）出现，不带别名、不是表达式。
//
// 执行后 AnalyzeGridQueryWithMetadata 仍会基于真实结果列做权威判定；
// 这里判错的最坏后果是"本可用 ROWID 编辑变成只读"（fail-closed），不会产生错误的写入。
func gridProjectionCoversPrimaryKey(kind string, parsed *ParsedGridQuery, fields []Field) bool {
	if parsed == nil || len(fields) == 0 {
		return false
	}
	pkCols := make([]string, 0, 4)
	for _, f := range fields {
		if f.PrimaryKey {
			pkCols = append(pkCols, strings.ToUpper(f.Name))
		}
	}
	if len(pkCols) == 0 {
		return false
	}
	if parsed.IsWildcard {
		return true
	}
	projected := make(map[string]bool, len(parsed.Projections))
	for _, item := range parsed.Projections {
		if name := gridBareProjectionColumn(kind, item); name != "" {
			projected[strings.ToUpper(name)] = true
		}
	}
	for _, col := range pkCols {
		if !projected[col] {
			return false
		}
	}
	return true
}

// prepareGridQuery 在占用流式查询连接之前完成与结果列无关的受限规划：
// 语法允许清单、ROWID 改写前置的普通堆表确认，以及编辑能力所需的元数据。
// 任何一步拿不到结果都只降级为只读，不影响原始查询的执行与读取能力（DB-02/DB-03）。
//
// DB-08: 投影已完整覆盖主键时不再需要 ROWID 定位列，因此跳过索引查询与堆表确认，
// 把最高频的"单表浏览"前置开销压到一次字段查询。
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

	// DB-08: 整段元数据规划共用**一个**总预算。旧实现给堆表确认与字段/索引各开一个
	// 串行预算（最坏叠加 ~3s），而实测单次字典查询冷启动就有 ~1.9s，
	// 分预算会把本来能成功的读取判成超时并静默降级为只读。
	budgetCtx, cancelBudget := gridMetadataBudgetContext(ctx, source)
	defer cancelBudget()

	// 字段元数据便宜（实测冷 ~10ms、稳态 ~0ms），且是"是否需要 ROWID"的前提，先取。
	fields, fieldsOK := m.gridFieldsFor(budgetCtx, source, schema, parsed.Table)
	if fieldsOK {
		prep.Fields = fields
	}
	// 投影已完整覆盖主键 → 行身份可以直接用主键，不需要 ROWID 定位列。
	pkCovered := gridProjectionCoversPrimaryKey(source.Kind, parsed, fields)

	// 普通堆表事实是 ROWID 改写的前提，也是 oracle_rowid 编辑能力的前提（DB-02）。
	// 查询失败或预算不足时保持未知，调用方按只读处理。
	// 主键已覆盖时 ROWID 定位列没有任何意义，因此跳过这次查询。
	if source.Kind == KindOracle && rowIDPlan != nil && !pkCovered {
		isHeap, known := m.isOracleHeapTable(budgetCtx, source, schema, parsed.Table)
		if known {
			prep.HeapKnown, prep.HeapTable = true, isHeap
			if isHeap {
				prep.RowIDSQL = rowIDPlan.SQL
			}
		}
	}
	if !needEditable {
		return prep
	}

	// DB-08: 目标必须是真实基表才可能有可插入的物理行。
	// 这次查询很便宜（实测冷 ~1ms、稳态 ~0ms），但它是"能不能新增行"的唯一依据，
	// 因此即使主键已覆盖也照常确认。
	if isBase, known := m.isBaseTable(budgetCtx, source, schema, parsed.Table); known {
		prep.BaseKnown, prep.BaseTable = true, isBase
	}

	// 索引只服务于"主键缺失时的唯一键定位"，主键已覆盖时不需要它（DB-08）。
	// 这是整段元数据里最贵的一次字典查询，跳过它对单表浏览的首批延迟影响最大。
	if !pkCovered {
		if indexes, ok := m.gridIndexesFor(budgetCtx, source, schema, parsed.Table); ok {
			prep.Indexes = indexes
		}
	}
	if !fieldsOK {
		if prep.Reason == "" {
			prep.Reason = fmt.Sprintf("无法在受限预算内读取目标基表 %s.%s 的元数据，已降级为只读", schema, parsed.Table)
		}
		return prep
	}
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
	// DB-04: 记录目标基表字段元数据（含未投影列），供 INSERT 按声明类型绑定。
	plan.TableFields = append([]Field(nil), fields...)

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
				// 声明类型以元数据为准，驱动上报的类型只作兜底（DB-04）。
				if strings.TrimSpace(phys.DataType) != "" {
					binding.DataType = phys.DataType
				}
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
		// P2（审核第 9 项）：`SELECT t.*, t.ID AS EXTRA_ID FROM LIVE t` 这类通配符与显式列
		// 混合投影，通配符会展开成若干结果列，投影项与结果列的下标不再一一对应：
		// 旧实现按投影下标硬绑，会把结果里的 AMOUNT 绑成物理列 ID（可写），
		// 提交时又会因为索引对不上而报错。无法可靠展开时直接关闭编辑。
		if gridHasWildcardProjection(parsed.Projections) {
			plan.Columns = bindings
			gridDisableEditing(plan, "通配符与显式列混合投影无法确定结果列与物理列的对应关系，已关闭网格编辑")
			return plan
		}
		if len(parsed.Projections) != len(resultColumns) {
			plan.Columns = bindings
			gridDisableEditing(plan, fmt.Sprintf(
				"查询投影列数(%d)与结果集列数(%d)不一致，无法确定物理列对应关系，已关闭网格编辑",
				len(parsed.Projections), len(resultColumns)))
			return plan
		}
		// 显式投影列匹配
		for i, item := range parsed.Projections {
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
					// 声明类型以元数据为准，驱动上报的类型只作兜底（DB-04）。
					if strings.TrimSpace(phys.DataType) != "" {
						binding.DataType = phys.DataType
					}
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

	// DBUI-01：名称协议下的重复别名/交叉别名无法安全定位目标列，必须整体只读。
	// 例如 `SELECT SALARY AS ID, ID AS SALARY` 会让值与定位键被解释成别的列。
	if ambiguity := gridColumnNameAmbiguity(bindings); ambiguity != "" {
		gridDisableEditing(plan, ambiguity)
		return plan
	}

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

	// DBUI-01/DB-06: 行尾隐藏定位列也纳入列绑定摘要，摘要下标与最终行数组下标严格对齐，
	// 且前端可据此确认“身份载荷确实已经交付”。
	if plan.IdentityPolicy == "oracle_rowid" && hiddenRowIDIdx >= 0 && hiddenRowIDIdx >= len(plan.Columns) {
		for len(plan.Columns) < hiddenRowIDIdx {
			plan.Columns = append(plan.Columns, GridColumnBinding{
				Index:          len(plan.Columns),
				Writable:       false,
				ReadOnlyReason: "结果列缺少服务端绑定",
			})
		}
		plan.Columns = append(plan.Columns, GridColumnBinding{
			Index:          hiddenRowIDIdx,
			ResultName:     "__KAIRO_EDIT_RID__",
			PhysicalName:   "ROWID",
			DataType:       "VARCHAR2",
			Writable:       false,
			ReadOnlyReason: "行尾隐藏定位列（服务端行身份），不参与网格写入",
		})
	}

	// 计划级能力必须反映到列级可写性：不能定位单行（不可更新）时，任何列都不可写，
	// 用户双击时就能看到原因。插入能力独立判定，不参与这里的推导。
	if !plan.CanUpdate {
		for i := range plan.Columns {
			plan.Columns[i].Writable = false
			if plan.Columns[i].ReadOnlyReason == "" {
				plan.Columns[i].ReadOnlyReason = plan.Reason
			}
		}
	}

	// DBUI-01: 插入能力必须独立判定，且不能由“单表可解析”推导 ——
	// 聚合/表达式投影不是可以直接新增行的表格视图，不得放开插入。
	//
	// DB-08: 还必须确认目标是**真实基表**。关联视图没有可插入的物理行：
	// 真实 Oracle 实测中 `SELECT * FROM KAIRO_LAB.EMPLOYEE_DIRECTORY`（JOIN 视图）
	// 在不可更新时仍然开放了 can_insert，用户新增行后只会撞上 ORA-01733。
	// 拿不到基表事实时同样不开放插入（fail-closed）。
	if source.MutationAllowed() && plan.Table != "" && gridProjectionAllowsInsert(parsed, plan.Columns) {
		switch {
		case !prep.BaseKnown:
			if plan.Reason == "" {
				plan.Reason = fmt.Sprintf("无法确认目标对象 %s.%s 是否为真实基表，已关闭插入能力", plan.Schema, plan.Table)
			}
		case !prep.BaseTable:
			if plan.Reason == "" {
				plan.Reason = fmt.Sprintf("目标对象 %s.%s 不是真实基表（视图/同义词等），不支持网格插入", plan.Schema, plan.Table)
			}
		default:
			plan.CanInsert = true
		}
	}

	return plan
}

// gridColumnNameAmbiguity 检测名称协议的歧义（DBUI-01）：
//   - 两个结果列名指向不同物理列 → 无法区分目标列；
//   - 某个结果列名恰好等于“另一个物理列名” → 交叉别名
//     （如 `SELECT SALARY AS ID, ID AS SALARY`），名称协议会把值与定位键解释成别的列。
//
// 返回非空原因表示整个结果集必须只读。
func gridColumnNameAmbiguity(bindings []GridColumnBinding) string {
	physNames := make(map[string]bool, len(bindings))
	for _, binding := range bindings {
		if name := strings.TrimSpace(binding.PhysicalName); name != "" {
			physNames[strings.ToUpper(name)] = true
		}
	}
	seen := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		resultName := strings.TrimSpace(binding.ResultName)
		physicalName := strings.TrimSpace(binding.PhysicalName)
		if resultName == "" || physicalName == "" {
			continue
		}
		key := strings.ToUpper(resultName)
		if prev, ok := seen[key]; ok && !strings.EqualFold(prev, physicalName) {
			return fmt.Sprintf("结果列名 %s 同时指向物理列 %s 与 %s，名称协议无法安全定位目标列", resultName, prev, physicalName)
		}
		seen[key] = physicalName
		if !strings.EqualFold(key, strings.ToUpper(physicalName)) && physNames[key] {
			return fmt.Sprintf("结果列名 %s 是交叉别名（真实来源 %s，同时另有同名列 %s），名称协议会把值与定位键解释成别的列", resultName, physicalName, resultName)
		}
	}
	return ""
}

// gridProjectionAllowsInsert 判定投影是否只由目标基表的直接物理列组成（DBUI-01）。
// 通配符查询天然成立；显式投影里只要出现聚合/表达式/常量列，就不开放插入。
//
// 只比较前 len(parsed.Projections) 个绑定：plan.Columns 末尾可能被追加"行尾隐藏定位列"
// （oracle_rowid 的 ROWID，Writable=false，见本文件 DBUI-01/DB-06 段）。把定位列也算进
// 投影会让 writableProjections 恒比 Projections 多 1，导致无主键表的显式投影列表
// 永远 CanInsert=false（回归：DB-01/DB-02 的旗舰场景被静默关掉插入能力）。
func gridProjectionAllowsInsert(parsed *ParsedGridQuery, bindings []GridColumnBinding) bool {
	if parsed == nil {
		return false
	}
	if parsed.IsWildcard {
		return true
	}
	limit := len(parsed.Projections)
	if limit == 0 || len(bindings) < limit {
		return false
	}
	for i := 0; i < limit; i++ {
		if strings.TrimSpace(bindings[i].PhysicalName) == "" {
			return false
		}
	}
	return true
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
