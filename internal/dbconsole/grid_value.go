package dbconsole

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// DB-04：按“声明的物理字段类型”做无损参数转换
// ---------------------------------------------------------------------------

// gridDeclaredTypeBase 归一化声明类型：去掉长度/精度括号并压平空白。
//
//	VARCHAR2(200)                     -> VARCHAR2
//	TIMESTAMP(6) WITH TIME ZONE        -> TIMESTAMP WITH TIME ZONE
//	NUMBER(20,6)                       -> NUMBER
func gridDeclaredTypeBase(declared string) string {
	upper := strings.ToUpper(strings.TrimSpace(declared))
	var bare strings.Builder
	depth := 0
	for i := 0; i < len(upper); i++ {
		switch upper[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				bare.WriteByte(upper[i])
			}
		}
	}
	return strings.Join(strings.Fields(bare.String()), " ")
}

func gridTypeIsText(base string) bool {
	if base == "" {
		return false
	}
	return strings.Contains(base, "CHAR") || strings.Contains(base, "CLOB") ||
		strings.Contains(base, "TEXT") || base == "STRING" || base == "UUID"
}

func gridTypeIsTime(base string) bool {
	if base == "" {
		return false
	}
	if base == "DATE" || base == "DATETIME" || base == "SMALLDATETIME" {
		return true
	}
	return strings.HasPrefix(base, "TIMESTAMP")
}

func gridTypeIsNumber(base string) bool {
	if base == "" {
		return false
	}
	switch base {
	case "NUMBER", "NUMERIC", "DECIMAL", "DEC", "INT", "INTEGER", "SMALLINT",
		"TINYINT", "MEDIUMINT", "BIGINT", "FLOAT", "DOUBLE", "DOUBLE PRECISION",
		"REAL", "MONEY", "SMALLMONEY", "BINARY_FLOAT", "BINARY_DOUBLE", "SERIAL":
		return true
	}
	return strings.HasPrefix(base, "INT") || strings.HasPrefix(base, "NUMBER") ||
		strings.HasPrefix(base, "DECIMAL") || strings.HasPrefix(base, "NUMERIC")
}

func gridTypeIsBinary(base string) bool {
	if base == "" {
		return false
	}
	switch base {
	case "RAW", "BLOB", "BINARY", "VARBINARY", "BYTEA", "IMAGE", "LONG RAW", "BFILE":
		return true
	}
	return strings.HasSuffix(base, "BLOB")
}

// gridTimeLayouts 日期字面量白名单：带 offset 的布局保留原时刻，
// 无时区布局用固定 UTC 承载，避免机器默认时区静默改动用户输入。
var gridTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999 -07:00",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00", // RFC3339Nano
	"2006-01-02T15:04:05Z07:00",           // RFC3339
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006/01/02 15:04:05.999999999",
	"2006/01/02 15:04:05",
	"2006-01-02",
	"2006/01/02",
}

// gridTimeParam 只在列被声明为 DATE/TIMESTAMP 时把字面量解析成 time.Time。
// 解析失败时保持原文，让数据库报出明确的转换错误，而不是我们凭空造一个值。
func gridTimeParam(val any) any {
	var text string
	switch v := val.(type) {
	case time.Time:
		return v
	case string:
		text = strings.TrimSpace(v)
	case []byte:
		text = strings.TrimSpace(string(v))
	default:
		return val
	}
	if text == "" {
		return val
	}
	candidates := []string{text}
	if strings.Contains(text, "/") {
		candidates = append(candidates, strings.ReplaceAll(text, "/", "-"))
	}
	for _, candidate := range candidates {
		for _, layout := range gridTimeLayouts {
			// 带 offset 的布局会使用字面量里的 offset；无时区布局固定落在 UTC。
			if parsed, err := time.ParseInLocation(layout, candidate, time.UTC); err == nil {
				return parsed
			}
		}
	}
	return val
}

// normalizeTypedParam 按声明的物理字段类型做无损类型转换（DB-04）。
//
// 关键约束：
//   - 只有声明为 DATE/TIMESTAMP 的列才做日期解析；VARCHAR2/CHAR/TEXT 一律原样传字符串，
//     不再按“字符串外形”猜测类型（旧实现会把文本 `2026-09-22 12:34:56` 变成 time.Time，
//     并把 `2026/09/22` 的斜杠替换掉）。
//   - json.Number 原样保留，十进制精度不经 float64 中转。
//   - 声明类型未知时不做任何推断。
func normalizeTypedParam(kind, declaredType string, val any) any {
	if val == nil {
		return nil
	}
	if number, ok := val.(json.Number); ok {
		return number
	}
	base := gridDeclaredTypeBase(declaredType)
	if gridTypeIsTime(base) {
		return gridTimeParam(val)
	}
	// 文本 / 二进制 / 数值 / 未知类型：保持调用方给的值（驱动按字符串或原生类型绑定）。
	return val
}

// ---------------------------------------------------------------------------
// DBUI-01：结果列名 ↔ 物理列名的不可变绑定
// ---------------------------------------------------------------------------

// gridPhysicalBinding 由 ResultEditContext.Columns 构建：
//   - byResult：结果列名 -> 物理列名（页面显示名只在名称协议里使用）
//   - byPhys：物理列名 -> 规范物理列名
//   - types：物理列名 -> 声明类型（DB-04 的类型绑定依据）
type gridPhysicalBinding struct {
	byResult map[string]string
	byPhys   map[string]string
	types    map[string]string
	writable map[string]bool
	reasons  map[string]string
	// tablePhys/tableTypes 来自服务端元数据确认的目标表字段（含未投影列），
	// 只作为 INSERT 的物理列与类型依据（DB-04）；UPDATE/DELETE 仍只认结果列绑定。
	tablePhys  map[string]string
	tableTypes map[string]string
}

func newGridPhysicalBinding(plan *ResultEditContext) (*gridPhysicalBinding, error) {
	binding := &gridPhysicalBinding{
		byResult:   make(map[string]string),
		byPhys:     make(map[string]string),
		types:      make(map[string]string),
		writable:   make(map[string]bool),
		reasons:    make(map[string]string),
		tablePhys:  make(map[string]string),
		tableTypes: make(map[string]string),
	}
	if plan == nil {
		return binding, nil
	}
	for _, field := range plan.TableFields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			continue
		}
		key := strings.ToUpper(name)
		binding.tablePhys[key] = name
		binding.tableTypes[key] = field.DataType
	}
	for _, col := range plan.Columns {
		phys := strings.TrimSpace(col.PhysicalName)
		if phys == "" {
			continue
		}
		key := strings.ToUpper(phys)
		if _, exists := binding.byPhys[key]; !exists {
			binding.byPhys[key] = phys
		}
		if strings.TrimSpace(col.DataType) != "" {
			binding.types[key] = col.DataType
		}
		binding.writable[key] = binding.writable[key] || col.Writable
		if col.ReadOnlyReason != "" {
			binding.reasons[key] = col.ReadOnlyReason
		}
		name := strings.TrimSpace(col.ResultName)
		if name == "" {
			continue
		}
		nameKey := strings.ToUpper(name)
		if prev, ok := binding.byResult[nameKey]; ok && !strings.EqualFold(prev, phys) {
			return nil, fmt.Errorf("结果列名 %s 同时出现在物理列 %s 与 %s，名称协议无法安全定位目标列", name, prev, phys)
		}
		binding.byResult[nameKey] = phys
	}
	return binding, nil
}

// tryResolve 把一个名称（结果列名或物理列名）解析为规范物理列名。
// 交叉别名（结果名恰好等于另一个物理列名）必须报错，避免把值和定位键解释成别的列。
func (b *gridPhysicalBinding) tryResolve(name string) (string, bool, error) {
	key := strings.ToUpper(strings.TrimSpace(name))
	if key == "" {
		return "", false, errors.New("列名不能为空")
	}
	fromPhys, hasPhys := b.byPhys[key]
	fromResult, hasResult := b.byResult[key]
	switch {
	case hasPhys && hasResult && !strings.EqualFold(fromPhys, fromResult):
		return "", false, fmt.Errorf("列 %s 存在交叉别名歧义（结果列名指向 %s，物理列名是 %s），拒绝按名称协议写入", name, fromResult, fromPhys)
	case hasResult:
		return fromResult, true, nil
	case hasPhys:
		return fromPhys, true, nil
	default:
		return "", false, nil
	}
}

func (b *gridPhysicalBinding) resolve(name string) (string, error) {
	phys, found, err := b.tryResolve(name)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("列 %s 不是目标基表列", name)
	}
	return phys, nil
}

func (b *gridPhysicalBinding) typeOf(phys string) string {
	key := strings.ToUpper(strings.TrimSpace(phys))
	if declared := b.types[key]; declared != "" {
		return declared
	}
	return b.tableTypes[key]
}

// resolveTableField 只在服务端元数据确认的目标表字段里解析（INSERT 用）。
func (b *gridPhysicalBinding) resolveTableField(name string) (string, bool) {
	phys, ok := b.tablePhys[strings.ToUpper(strings.TrimSpace(name))]
	return phys, ok
}

func (b *gridPhysicalBinding) writableOf(phys string) (bool, string) {
	key := strings.ToUpper(strings.TrimSpace(phys))
	reason := b.reasons[key]
	if reason == "" {
		reason = "该列不允许网格修改"
	}
	return b.writable[key], reason
}

// gridNormalizeAction 入口唯一规范化：大小写/空白变体不得通过前半段却跳过后半段检查（DB-05）。
func gridNormalizeAction(action string) string {
	normalized := strings.ToLower(strings.TrimSpace(action))
	switch normalized {
	case "insert", "update", "delete":
		return normalized
	default:
		return ""
	}
}

// gridSameCellValue 比较两个快照单元格是否语义相同（JSON 数字/原生数字互相兼容）。
func gridSameCellValue(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// gridMutationIntent 归一化后的单行变更意图：键一律是规范物理列名（DBUI-01）。
type gridMutationIntent struct {
	values   map[string]any // 物理列 -> 新值
	key      map[string]any // 物理列 -> 定位原值
	snapshot map[string]any // 物理列 -> original 原值快照
	rowID    string
}

// locatorValue 读取定位列原值，并对“key 里的定位原值”与“original 快照”做一致性校验（DB-05）。
func (intent *gridMutationIntent) locatorValue(phys string) (any, bool, error) {
	keyValue, hasKey := findMapValueInsensitive(intent.key, phys)
	snapshotValue, hasSnapshot := findMapValueInsensitive(intent.snapshot, phys)
	if hasKey && hasSnapshot && !gridSameCellValue(keyValue, snapshotValue) {
		return nil, false, fmt.Errorf("定位列 %s 的原值与 original 快照不一致，同一请求不得携带两套快照", phys)
	}
	if hasKey {
		return keyValue, true, nil
	}
	if hasSnapshot {
		return snapshotValue, true, nil
	}
	return nil, false, nil
}

// gridValueProvided 判断索引协议里某项是否真的带值（has_value 缺失时按 value 非空回退）。
func gridValueProvided(hasValue bool, value any) bool {
	return hasValue || value != nil
}

// gridBuildIntent 把前端负载归一化成物理列名键的变更意图。
//
// 优先使用索引协议（Changes + RowColumns），服务器凭不可变 Columns 解析物理列；
// 否则退回名称协议，并同时映射 values / original / key 三者（DBUI-01）。
func gridBuildIntent(plan *ResultEditContext, binding *gridPhysicalBinding, action string, mutation GridMutation) (*gridMutationIntent, error) {
	intent := &gridMutationIntent{
		values:   make(map[string]any),
		key:      make(map[string]any),
		snapshot: make(map[string]any),
		rowID:    strings.TrimSpace(mutation.RowID),
	}

	if len(mutation.Changes) > 0 && action != "insert" {
		byIndex := make(map[int]GridColumnBinding, len(plan.Columns))
		for _, col := range plan.Columns {
			byIndex[col.Index] = col
		}
		for _, row := range mutation.RowColumns {
			col, ok := byIndex[row.ColumnIndex]
			if !ok {
				return nil, fmt.Errorf("结果列索引 %d 不存在于当前编辑上下文", row.ColumnIndex)
			}
			if !gridValueProvided(row.HasValue, row.Value) {
				continue
			}
			// 非基表列（表达式/聚合）没有物理目标，不参与快照；它们也永远不可写。
			if strings.TrimSpace(col.PhysicalName) == "" {
				continue
			}
			phys, err := binding.resolve(col.PhysicalName)
			if err != nil {
				return nil, err
			}
			if prev, exists := intent.snapshot[phys]; exists && !gridSameCellValue(prev, row.Value) {
				return nil, fmt.Errorf("物理列 %s 出现了两个不同的原值快照（结果列索引 %d），拒绝提交", phys, row.ColumnIndex)
			}
			intent.snapshot[phys] = row.Value
			intent.key[phys] = row.Value
		}
		for _, change := range mutation.Changes {
			col, ok := byIndex[change.ColumnIndex]
			if !ok {
				return nil, fmt.Errorf("结果列索引 %d 不存在于当前编辑上下文", change.ColumnIndex)
			}
			if !gridValueProvided(change.HasValue, change.Value) {
				continue
			}
			if !col.Writable {
				reason := col.ReadOnlyReason
				if reason == "" {
					reason = "该列不允许网格修改"
				}
				return nil, fmt.Errorf("列 %s 不允许网格修改: %s", gridBindingLabel(col), reason)
			}
			if strings.TrimSpace(col.PhysicalName) == "" {
				return nil, fmt.Errorf("列 %s 不是目标基表物理列", gridBindingLabel(col))
			}
			phys, err := binding.resolve(col.PhysicalName)
			if err != nil {
				return nil, err
			}
			if change.HasOriginal {
				if prev, exists := intent.snapshot[phys]; exists && !gridSameCellValue(prev, change.Original) {
					return nil, fmt.Errorf("物理列 %s 的 original 与整行快照不一致，拒绝提交", phys)
				}
				if _, exists := intent.snapshot[phys]; !exists {
					intent.snapshot[phys] = change.Original
				}
			}
			intent.values[phys] = change.Value
		}
		return intent, nil
	}

	// 名称协议：values / original / key 必须一起映射到同一批物理列。
	for name, value := range mutation.Values {
		phys, err := binding.resolve(name)
		if err != nil {
			// INSERT 允许写入未投影但已由元数据确认的目标表字段（DB-04）。
			if action != "insert" {
				return nil, err
			}
			field, ok := binding.resolveTableField(name)
			if !ok {
				return nil, err
			}
			phys = field
		}
		// 服务器是可写性的最终权威：计算列/LOB/行身份列一律拒绝（DBUI-01）。
		if action != "insert" {
			if writable, reason := binding.writableOf(phys); !writable {
				return nil, fmt.Errorf("列 %s 不允许网格修改: %s", phys, reason)
			}
		}
		intent.values[phys] = value
	}
	for name, value := range mutation.Original {
		if strings.EqualFold(strings.TrimSpace(name), "__KAIRO_EDIT_RID__") {
			if intent.rowID == "" && value != nil {
				intent.rowID = fmt.Sprint(value)
			}
			continue
		}
		phys, err := binding.resolve(name)
		if err != nil {
			return nil, err
		}
		intent.snapshot[phys] = value
	}
	keySource := mutation.Key
	if len(keySource) == 0 {
		keySource = mutation.Original
	}
	for name, value := range keySource {
		if strings.EqualFold(strings.TrimSpace(name), "__KAIRO_EDIT_RID__") {
			if intent.rowID == "" && value != nil {
				intent.rowID = fmt.Sprint(value)
			}
			continue
		}
		phys, err := binding.resolve(name)
		if err != nil {
			return nil, err
		}
		intent.key[phys] = value
	}
	if v, ok := mutation.Original["__KAIRO_EDIT_RID__"]; ok && intent.rowID == "" && v != nil {
		intent.rowID = fmt.Sprint(v)
	}
	return intent, nil
}

func gridBindingLabel(col GridColumnBinding) string {
	if strings.TrimSpace(col.ResultName) != "" {
		return col.ResultName
	}
	if strings.TrimSpace(col.PhysicalName) != "" {
		return col.PhysicalName
	}
	return fmt.Sprintf("#%d", col.Index)
}

// gridParamBuilder 统一管理占位符与绑定参数，确保 SET 参数先于 WHERE 参数（DB-04）。
type gridParamBuilder struct {
	kind       string
	binding    *gridPhysicalBinding
	args       []any
	paramIndex int
}

func (b *gridParamBuilder) marker(phys string, value any) string {
	converted := normalizeTypedParam(b.kind, b.binding.typeOf(phys), value)
	b.paramIndex++
	if b.kind == KindOracle {
		name := fmt.Sprintf("kairo_p%d", b.paramIndex)
		b.args = append(b.args, sql.Named(name, converted))
		return ":" + name
	}
	b.args = append(b.args, converted)
	return "?"
}

// BuildGridMutationSQLWithPlan 基于服务端编辑计划构建参数化单行变更 SQL
func BuildGridMutationSQLWithPlan(kind string, plan *ResultEditContext, mutation GridMutation) (string, []any, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != KindOracle && kind != KindMySQL {
		return "", nil, errors.New("网格编辑仅支持 Oracle/MySQL")
	}
	if plan == nil {
		return "", nil, errors.New("缺少网格编辑计划上下文")
	}

	// DB-05：action 在入口唯一规范化，后续所有判定只读这个结果。
	action := gridNormalizeAction(mutation.Action)
	if action == "" {
		return "", nil, errors.New("网格操作仅支持 insert/update/delete")
	}

	tableSQL, err := gridQualifiedTable(kind, plan.Schema, plan.Table)
	if err != nil {
		return "", nil, err
	}

	// 能力判定：插入/更新/删除彼此独立，绝不由相邻能力推导（DBUI-01）。
	switch action {
	case "insert":
		if !plan.CanInsert {
			return "", nil, fmt.Errorf("当前表不支持插入: %s", plan.Reason)
		}
	case "update":
		if !plan.CanUpdate {
			return "", nil, fmt.Errorf("当前表不支持更新: %s", plan.Reason)
		}
	case "delete":
		if !plan.CanDelete {
			return "", nil, fmt.Errorf("当前表不支持删除: %s", plan.Reason)
		}
	}

	binding, err := newGridPhysicalBinding(plan)
	if err != nil {
		return "", nil, err
	}
	intent, err := gridBuildIntent(plan, binding, action, mutation)
	if err != nil {
		return "", nil, err
	}
	params := &gridParamBuilder{kind: kind, binding: binding}

	switch action {
	case "insert":
		keys := sortedGridKeys(intent.values)
		if len(keys) == 0 {
			return "", nil, errors.New("insert 至少需要一列")
		}
		cols := make([]string, 0, len(keys))
		vals := make([]string, 0, len(keys))
		for _, phys := range keys {
			col, qErr := quoteGridIdentifier(kind, phys, "列名")
			if qErr != nil {
				return "", nil, qErr
			}
			cols = append(cols, col)
			vals = append(vals, params.marker(phys, intent.values[phys]))
		}
		return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", tableSQL, strings.Join(cols, ", "), strings.Join(vals, ", ")), params.args, nil

	case "update":
		keys := sortedGridKeys(intent.values)
		if len(keys) == 0 {
			return "", nil, errors.New("update 至少需要一列")
		}
		sets := make([]string, 0, len(keys))
		for _, phys := range keys {
			col, qErr := quoteGridIdentifier(kind, phys, "列名")
			if qErr != nil {
				return "", nil, qErr
			}
			sets = append(sets, col+" = "+params.marker(phys, intent.values[phys]))
		}
		whereSQL, whereErr := gridWhereClause(kind, plan, action, intent, params)
		if whereErr != nil {
			return "", nil, whereErr
		}
		return fmt.Sprintf("UPDATE %s SET %s WHERE %s", tableSQL, strings.Join(sets, ", "), whereSQL), params.args, nil

	default: // delete
		whereSQL, whereErr := gridWhereClause(kind, plan, action, intent, params)
		if whereErr != nil {
			return "", nil, whereErr
		}
		return fmt.Sprintf("DELETE FROM %s WHERE %s", tableSQL, whereSQL), params.args, nil
	}
}

// gridWhereClause 使用编辑计划中确认的真实定位键与原值构造 WHERE 子句。
//
// DB-05：locatorColumns 只记录本次真正加入 WHERE 的物理定位列；只有它们才可以
// 跳过重复的 original 比较。ROWID 定位时该集合不含任何业务列，因此所有被修改的
// 业务列（包括主键列）都必须继续比较原值。
func gridWhereClause(kind string, plan *ResultEditContext, action string, intent *gridMutationIntent, params *gridParamBuilder) (string, error) {
	parts := make([]string, 0, 8)
	locatorColumns := make(map[string]bool, 4)

	switch plan.IdentityPolicy {
	case "oracle_rowid":
		if kind != KindOracle {
			return "", errors.New("非 Oracle 数据源不支持 ROWID 定位")
		}
		rowID := intent.rowID
		if strings.TrimSpace(rowID) == "" {
			if v, ok := findMapValueInsensitive(intent.key, "__KAIRO_EDIT_RID__"); ok && v != nil {
				rowID = fmt.Sprint(v)
			}
		}
		if strings.TrimSpace(rowID) == "" || strings.ContainsAny(rowID, "\x00\r\n'\"") {
			return "", errors.New("缺少有效的 Oracle ROWID")
		}
		parts = append(parts, "ROWID = "+params.marker("ROWID", rowID))

	case "pk":
		if len(plan.PrimaryKeys) == 0 {
			return "", errors.New("目标表未提供完整主键元数据")
		}
		for _, pkCol := range plan.PrimaryKeys {
			value, found, err := intent.locatorValue(pkCol)
			if err != nil {
				return "", err
			}
			if !found || value == nil {
				return "", fmt.Errorf("主键列 %s 缺失或为 NULL，无法唯一定位行", pkCol)
			}
			quoted, qErr := quoteGridIdentifier(kind, pkCol, "主键列")
			if qErr != nil {
				return "", qErr
			}
			parts = append(parts, quoted+" = "+params.marker(pkCol, value))
			locatorColumns[strings.ToUpper(pkCol)] = true
		}

	case "unique":
		if len(plan.UniqueKeys) == 0 {
			return "", errors.New("目标表未提供完整非空唯一键元数据")
		}
		for _, uCol := range plan.UniqueKeys {
			value, found, err := intent.locatorValue(uCol)
			if err != nil {
				return "", err
			}
			if !found || value == nil {
				return "", fmt.Errorf("唯一键列 %s 缺失或为 NULL，无法唯一定位行", uCol)
			}
			quoted, qErr := quoteGridIdentifier(kind, uCol, "唯一键列")
			if qErr != nil {
				return "", qErr
			}
			parts = append(parts, quoted+" = "+params.marker(uCol, value))
			locatorColumns[strings.ToUpper(uCol)] = true
		}

	default:
		return "", fmt.Errorf("当前结果集缺少有效行定位策略 (%s): %s", plan.IdentityPolicy, plan.Reason)
	}

	// 乐观并发原值条件：只跳过真正进入 WHERE 的定位列。
	if action == "update" {
		if len(intent.snapshot) == 0 {
			return "", errors.New("更新操作必须提供原值 original 以执行并发冲突检测")
		}
		for _, phys := range sortedGridKeys(intent.values) {
			origVal, found := findMapValueInsensitive(intent.snapshot, phys)
			if !found {
				return "", fmt.Errorf("修改列 %s 必须在 original 中提供原值", phys)
			}
			if locatorColumns[strings.ToUpper(phys)] {
				continue
			}
			quoted, qErr := quoteGridIdentifier(kind, phys, "原值列")
			if qErr != nil {
				return "", qErr
			}
			if origVal == nil {
				parts = append(parts, quoted+" IS NULL")
				continue
			}
			parts = append(parts, quoted+" = "+params.marker(phys, origVal))
		}
	} else if action == "delete" {
		// 删除时比较用户提供的完整快照（同样只跳过已进入 WHERE 的定位列）。
		if len(intent.snapshot) > 0 {
			for _, phys := range sortedGridKeys(intent.snapshot) {
				if locatorColumns[strings.ToUpper(phys)] || strings.EqualFold(phys, "ROWID") {
					continue
				}
				quoted, qErr := quoteGridIdentifier(kind, phys, "原值列")
				if qErr != nil {
					continue
				}
				origVal := intent.snapshot[phys]
				if origVal == nil {
					parts = append(parts, quoted+" IS NULL")
					continue
				}
				parts = append(parts, quoted+" = "+params.marker(phys, origVal))
			}
		}
	}

	return strings.Join(parts, " AND "), nil
}

func findMapValueInsensitive(m map[string]any, targetKey string) (any, bool) {
	if m == nil {
		return nil, false
	}
	if val, ok := m[targetKey]; ok {
		return val, true
	}
	targetUpper := strings.ToUpper(targetKey)
	for k, v := range m {
		if strings.ToUpper(k) == targetUpper {
			return v, true
		}
	}
	return nil, false
}

func isColumnInSlice(col string, list []string) bool {
	colUpper := strings.ToUpper(col)
	for _, item := range list {
		if strings.ToUpper(item) == colUpper {
			return true
		}
	}
	return false
}

// EncodeLosslessCell 检查整数是否超出 JS 精度安全界限并转换为字符串
func EncodeLosslessCell(val any) any {
	switch v := val.(type) {
	case int64:
		if v > 9007199254740991 || v < -9007199254740991 {
			return strconv.FormatInt(v, 10)
		}
		return v
	case uint64:
		if v > 9007199254740991 {
			return strconv.FormatUint(v, 10)
		}
		return int64(v)
	default:
		return val
	}
}

// Ensure driver.Value interface compliance
var _ driver.Valuer = (*ResultEditContext)(nil)

func (ctx *ResultEditContext) Value() (driver.Value, error) {
	if ctx == nil {
		return nil, nil
	}
	return ctx.ResultID, nil
}
