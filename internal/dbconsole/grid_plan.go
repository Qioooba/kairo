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
	complexQueryRe = regexp.MustCompile(`(?is)\b(JOIN|UNION|INTERSECT|MINUS|EXCEPT|GROUP\s+BY|HAVING)\b`)
	distinctRe     = regexp.MustCompile(`(?is)^\s*SELECT\s+DISTINCT\b`)
	cteRe          = regexp.MustCompile(`(?is)^\s*WITH\b`)
	derivedFromRe  = regexp.MustCompile(`(?is)\bFROM\s*\(`)
)

// ParsedGridQuery 包含从 SQL 语法中提取的单表结构
type ParsedGridQuery struct {
	Schema       string
	Table        string
	TableAlias   string
	IsWildcard   bool
	Projections  []string
	HasTopOrder  bool
	WhereClause  string
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
		return nil, errors.New("JOIN、聚合或复合查询不支持网格编辑")
	}

	// 提取列投影
	items, err := extractSelectProjections(clean)
	if err != nil {
		return nil, err
	}

	// 提取 FROM 表与别名
	schema, table, alias, err := parseGridFromClause(kind, clean)
	if err != nil {
		return nil, err
	}

	isWildcard := len(items) == 1 && (items[0] == "*" || strings.HasSuffix(items[0], ".*"))

	return &ParsedGridQuery{
		Schema:      schema,
		Table:       table,
		TableAlias:  alias,
		IsWildcard:  isWildcard,
		Projections: items,
		HasTopOrder: QueryHasTopLevelOrderBy(kind, clean),
	}, nil
}

// parseGridFromClause 提取 FROM 子句中的单一基表，排除逗号隐式连接
func parseGridFromClause(kind, noComments string) (schema, table, alias string, err error) {
	match := fromTableRe.FindStringSubmatch(noComments)
	if len(match) < 2 || strings.TrimSpace(match[1]) == "" {
		return "", "", "", errors.New("无法定位查询的 FROM 基表")
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
		return "", "", "", errors.New("多表笛卡尔积或隐式连接不支持网格编辑")
	}

	// 提取表别名（如果有）
	aliasWords := strings.Fields(strings.TrimSpace(beforeNextClause))
	if len(aliasWords) > 0 {
		firstWord := strings.ToUpper(aliasWords[0])
		if firstWord == "AS" {
			if len(aliasWords) > 1 {
				alias = strings.Trim(aliasWords[1], `"`+"`")
			}
		} else if isIdent(aliasWords[0]) {
			alias = strings.Trim(aliasWords[0], `"`+"`")
		}
	}

	// 拆分 schema.table
	schema, table = SplitSchemaObject(fullTarget)
	if strings.EqualFold(table, "DUAL") {
		return "", "", "", errors.New("DUAL 伪表不支持网格编辑")
	}

	return schema, table, alias, nil
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

// RewriteOracleQueryForHiddenRowID 为 Oracle 单表受限查询重写投影，增加 ROWIDTOCHAR 隐藏定位列
func RewriteOracleQueryForHiddenRowID(sqlText string) (string, error) {
	trimmed := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sqlText), "; \t\r\n"))
	if trimmed == "" {
		return "", errors.New("SQL 不能为空")
	}
	if strings.Contains(trimmed, "__KAIRO_EDIT_RID__") {
		return trimmed, nil
	}
	clean := stripSQLComments(trimmed)
	parsed, err := parseGridQuerySyntax(KindOracle, clean)
	if err != nil {
		return sqlText, err
	}

	fromIdx := findTopLevelFromKeyword(clean)
	if fromIdx < 0 {
		return sqlText, errors.New("无法定位 FROM 关键字")
	}

	projStr := strings.TrimSpace(clean[6:fromIdx])
	targetQualifier := parsed.Table
	if parsed.TableAlias != "" {
		targetQualifier = parsed.TableAlias
	}
	quotedQualifier, qErr := quoteGridIdentifier(KindOracle, targetQualifier, "表别名")
	if qErr != nil {
		return sqlText, qErr
	}

	var newProj string
	if parsed.IsWildcard {
		if strings.HasSuffix(projStr, ".*") {
			newProj = projStr + `, ROWIDTOCHAR(` + quotedQualifier + `.ROWID) AS "__KAIRO_EDIT_RID__"`
		} else {
			newProj = quotedQualifier + `.*, ROWIDTOCHAR(` + quotedQualifier + `.ROWID) AS "__KAIRO_EDIT_RID__"`
		}
	} else {
		newProj = projStr + `, ROWIDTOCHAR(` + quotedQualifier + `.ROWID) AS "__KAIRO_EDIT_RID__"`
	}

	return "SELECT " + newProj + " " + strings.TrimSpace(clean[fromIdx:]), nil
}

// SetCachedHeapTable 设置 Oracle 普通堆表缓存（用于测试或快速判定）
func (m *Manager) SetCachedHeapTable(sourceID, schema, table string, isHeap bool) {
	cacheKey := fmt.Sprintf("%s\x00heap_table\x00%s\x00%s", sourceID, strings.ToUpper(schema), strings.ToUpper(table))
	metadataCacheSet(m, cacheKey, isHeap)
}

func (m *Manager) isOracleHeapTable(ctx context.Context, source Source, schema, table string) bool {
	if source.Kind != KindOracle {
		return false
	}
	cacheKey := fmt.Sprintf("%s\x00heap_table\x00%s\x00%s", source.ID, strings.ToUpper(schema), strings.ToUpper(table))
	if cached, ok := metadataCacheGet[bool](m, cacheKey); ok {
		return cached
	}
	isHeap := true
	_ = m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var count int
		err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM all_tables WHERE (owner = :1 OR UPPER(owner) = UPPER(:1)) AND (table_name = :2 OR UPPER(table_name) = UPPER(:2)) AND temporary = 'N' AND iot_type IS NULL`, schema, table).Scan(&count)
		if err == nil {
			isHeap = (count > 0)
		}
		return nil
	})
	metadataCacheSet(m, cacheKey, isHeap)
	return isHeap
}

// AnalyzeGridQuery 分析查询并建立完整的 ResultEditContext
func (m *Manager) AnalyzeGridQuery(ctx context.Context, source Source, sessionID, executedSQL string, resultColumns []Column, hiddenRowID ...int) (*ResultEditContext, error) {
	hiddenRowIDIdx := -1
	if len(hiddenRowID) > 0 {
		hiddenRowIDIdx = hiddenRowID[0]
	}
	resultID := generateResultID()
	plan := &ResultEditContext{
		ResultID:          resultID,
		SessionID:         sessionID,
		SourceID:          source.ID,
		SourceFingerprint: sourceFingerprint(source),
		Dialect:           source.Kind,
		ExecutedSQL:       executedSQL,
		IdentityPolicy:    "none",
		HiddenRowIDIndex:  hiddenRowIDIdx,
		CreatedAt:         time.Now(),
	}

	parsed, err := parseGridQuerySyntax(source.Kind, executedSQL)
	if err != nil {
		plan.Reason = err.Error()
		return plan, nil
	}

	plan.HasTopLevelOrder = parsed.HasTopOrder
	plan.TableAlias = parsed.TableAlias

	// 解析真实 Schema
	schema := parsed.Schema
	if schema == "" {
		schema = ResolveSchema(source, "")
	}
	plan.Schema = schema
	plan.Table = parsed.Table

	// 读取基表元数据字段
	fields, fErr := m.Fields(ctx, source, schema, parsed.Table)
	if fErr != nil || len(fields) == 0 {
		plan.Reason = fmt.Sprintf("无法读取目标基表 %s.%s 的元数据: %v", schema, parsed.Table, fErr)
		return plan, nil
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
	indexes, _ := m.Indexes(ctx, source, schema, parsed.Table)
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

	// Phase 1: 若未满足完整 PK/Unique 键，但为受支持的 Oracle 普通堆表且已取得隐藏 ROWID
	if plan.IdentityPolicy == "none" && source.Kind == KindOracle && hiddenRowIDIdx >= 0 {
		if m.isOracleHeapTable(ctx, source, schema, parsed.Table) {
			plan.IdentityPolicy = "oracle_rowid"
			plan.HiddenRowIDIndex = hiddenRowIDIdx
			plan.CanUpdate = true
			plan.CanDelete = true
			plan.Reason = ""
		} else {
			plan.Reason = "目标表非受支持 Oracle 普通堆表（如 IOT/临时表/视图），无主键时不支持 ROWID 编辑"
		}
	}

	// 只要单表物理来源明确，插入无需依赖已有行身份
	if source.MutationAllowed() && plan.Table != "" {
		plan.CanInsert = true
	}

	return plan, nil
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
