package dbconsole

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// normalizeTypedParam 针对不同数据库驱动做无损类型转换
func normalizeTypedParam(kind string, val any) any {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case string:
		// 检查是否为标准日期时间格式，并由驱动以 time.Time 绑定，避免 Oracle NLS 隐式转换
		if len(v) >= 19 && (strings.Contains(v, "-") || strings.Contains(v, "/")) && strings.Contains(v, ":") {
			clean := strings.ReplaceAll(v, "/", "-")
			layouts := []string{
				"2006-01-02 15:04:05",
				"2006-01-02 15:04:05.999999999",
				time.RFC3339,
				time.RFC3339Nano,
			}
			for _, layout := range layouts {
				if t, err := time.Parse(layout, clean); err == nil {
					return t
				}
			}
		}
		return v
	case int64:
		return v
	case float64:
		// 检查是否为整数
		if v == float64(int64(v)) {
			return int64(v)
		}
		return v
	default:
		return val
	}
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

	tableSQL, err := gridQualifiedTable(kind, plan.Schema, plan.Table)
	if err != nil {
		return "", nil, err
	}

	action := strings.ToLower(strings.TrimSpace(mutation.Action))
	if action != "insert" && action != "update" && action != "delete" {
		return "", nil, errors.New("网格操作仅支持 insert/update/delete")
	}

	args := make([]any, 0)
	var sqlText strings.Builder
	paramIndex := 0

	markerWithValue := func(value any) string {
		converted := normalizeTypedParam(kind, value)
		if kind == KindOracle {
			paramIndex++
			args = append(args, sql.Named(fmt.Sprintf("kairo_p%d", paramIndex), converted))
			return fmt.Sprintf(":kairo_p%d", paramIndex)
		}
		paramIndex++
		args = append(args, converted)
		return "?"
	}

	if action == "insert" {
		if !plan.CanInsert {
			return "", nil, fmt.Errorf("当前表不支持插入: %s", plan.Reason)
		}
		keys := sortedGridKeys(mutation.Values)
		if len(keys) == 0 {
			return "", nil, errors.New("insert 至少需要一列")
		}
		cols := make([]string, 0, len(keys))
		vals := make([]string, 0, len(keys))
		for _, key := range keys {
			col, qErr := quoteGridIdentifier(kind, key, "列名")
			if qErr != nil {
				return "", nil, qErr
			}
			cols = append(cols, col)
			vals = append(vals, markerWithValue(mutation.Values[key]))
		}
		fmt.Fprintf(&sqlText, "INSERT INTO %s (%s) VALUES (%s)", tableSQL, strings.Join(cols, ", "), strings.Join(vals, ", "))
		return sqlText.String(), args, nil
	}

	if action == "delete" {
		if !plan.CanDelete {
			return "", nil, fmt.Errorf("当前表不支持删除: %s", plan.Reason)
		}
		whereSQL, whereArgs, whereErr := buildGridWhereWithPlan(kind, plan, mutation, 0)
		if whereErr != nil {
			return "", nil, whereErr
		}
		args = append(args, whereArgs...)
		return fmt.Sprintf("DELETE FROM %s WHERE %s", tableSQL, whereSQL), args, nil
	}

	// update 路径
	if !plan.CanUpdate {
		return "", nil, fmt.Errorf("当前表不支持更新: %s", plan.Reason)
	}
	keys := sortedGridKeys(mutation.Values)
	if len(keys) == 0 {
		return "", nil, errors.New("update 至少需要一列")
	}

	// 验证修改的列是否合法可写
	bindingMap := make(map[string]GridColumnBinding, len(plan.Columns))
	for _, col := range plan.Columns {
		if col.PhysicalName != "" {
			bindingMap[strings.ToUpper(col.PhysicalName)] = col
		}
	}

	sets := make([]string, 0, len(keys))
	args = args[:0]
	paramIndex = 0

	for _, key := range keys {
		binding, ok := bindingMap[strings.ToUpper(key)]
		if !ok {
			return "", nil, fmt.Errorf("列 %s 不是目标基表列", key)
		}
		if !binding.Writable {
			return "", nil, fmt.Errorf("列 %s 不允许网格修改: %s", key, binding.ReadOnlyReason)
		}
		col, qErr := quoteGridIdentifier(kind, binding.PhysicalName, "列名")
		if qErr != nil {
			return "", nil, qErr
		}
		sets = append(sets, col+" = "+markerWithValue(mutation.Values[key]))
	}

	whereSQL, whereArgs, whereErr := buildGridWhereWithPlan(kind, plan, mutation, len(keys))
	if whereErr != nil {
		return "", nil, whereErr
	}
	args = append(args, whereArgs...)
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s", tableSQL, strings.Join(sets, ", "), whereSQL), args, nil
}

// buildGridWhereWithPlan 使用编辑计划中确认的真实定位键与原值构造 WHERE 子句
func buildGridWhereWithPlan(kind string, plan *ResultEditContext, mutation GridMutation, offset int) (string, []any, error) {
	parts := make([]string, 0, 8)
	args := make([]any, 0, 8)
	paramIndex := offset

	markerWithValue := func(value any) string {
		converted := normalizeTypedParam(kind, value)
		if kind == KindOracle {
			paramIndex++
			args = append(args, sql.Named(fmt.Sprintf("kairo_p%d", paramIndex), converted))
			return fmt.Sprintf(":kairo_p%d", paramIndex)
		}
		paramIndex++
		args = append(args, converted)
		return "?"
	}

	keyValues := mutation.Key
	if len(keyValues) == 0 {
		keyValues = mutation.Original
	}

	// 1. 定位条件 (ROWID / PK / Unique)
	switch plan.IdentityPolicy {
	case "oracle_rowid":
		if kind != KindOracle {
			return "", nil, errors.New("非 Oracle 数据源不支持 ROWID 定位")
		}
		rowID := mutation.RowID
		if rowID == "" && keyValues != nil {
			if v, ok := keyValues["__KAIRO_EDIT_RID__"]; ok && v != nil {
				rowID = fmt.Sprint(v)
			}
		}
		if strings.TrimSpace(rowID) == "" || strings.ContainsAny(rowID, "\x00\r\n'\"") {
			return "", nil, errors.New("缺少有效的 Oracle ROWID")
		}
		parts = append(parts, "ROWID = "+markerWithValue(rowID))

	case "pk":
		if len(plan.PrimaryKeys) == 0 {
			return "", nil, errors.New("目标表未提供完整主键元数据")
		}
		for _, pkCol := range plan.PrimaryKeys {
			val, found := findMapValueInsensitive(keyValues, pkCol)
			if !found || val == nil {
				return "", nil, fmt.Errorf("主键列 %s 缺失或为 NULL，无法唯一定位行", pkCol)
			}
			quoted, qErr := quoteGridIdentifier(kind, pkCol, "主键列")
			if qErr != nil {
				return "", nil, qErr
			}
			parts = append(parts, quoted+" = "+markerWithValue(val))
		}

	case "unique":
		if len(plan.UniqueKeys) == 0 {
			return "", nil, errors.New("目标表未提供完整非空唯一键元数据")
		}
		for _, uCol := range plan.UniqueKeys {
			val, found := findMapValueInsensitive(keyValues, uCol)
			if !found || val == nil {
				return "", nil, fmt.Errorf("唯一键列 %s 缺失或为 NULL，无法唯一定位行", uCol)
			}
			quoted, qErr := quoteGridIdentifier(kind, uCol, "唯一键列")
			if qErr != nil {
				return "", nil, qErr
			}
			parts = append(parts, quoted+" = "+markerWithValue(val))
		}

	default:
		return "", nil, fmt.Errorf("当前结果集缺少有效行定位策略 (%s): %s", plan.IdentityPolicy, plan.Reason)
	}

	// 2. 乐观并发原值条件 (Original Check)
	if mutation.Action == "update" {
		if len(mutation.Original) == 0 {
			return "", nil, errors.New("更新操作必须提供原值 original 以执行并发冲突检测")
		}

		// 必须为每一个修改的字段提供原值
		for valKey := range mutation.Values {
			origVal, found := findMapValueInsensitive(mutation.Original, valKey)
			if !found {
				return "", nil, fmt.Errorf("修改列 %s 必须在 original 中提供原值", valKey)
			}
			// 如果该列已经在主键中，无需重复添加
			if isColumnInSlice(valKey, plan.PrimaryKeys) || isColumnInSlice(valKey, plan.UniqueKeys) {
				continue
			}

			quoted, qErr := quoteGridIdentifier(kind, valKey, "原值列")
			if qErr != nil {
				return "", nil, qErr
			}
			if origVal == nil {
				parts = append(parts, quoted+" IS NULL")
			} else {
				parts = append(parts, quoted+" = "+markerWithValue(origVal))
			}
		}
	} else if mutation.Action == "delete" {
		// 删除时，将提供的全部有效原值加入校验（若有）
		if len(mutation.Original) > 0 {
			for origKey, origVal := range mutation.Original {
				if isColumnInSlice(origKey, plan.PrimaryKeys) || isColumnInSlice(origKey, plan.UniqueKeys) || strings.EqualFold(origKey, "__KAIRO_EDIT_RID__") {
					continue
				}
				quoted, qErr := quoteGridIdentifier(kind, origKey, "原值列")
				if qErr != nil {
					continue
				}
				if origVal == nil {
					parts = append(parts, quoted+" IS NULL")
				} else {
					parts = append(parts, quoted+" = "+markerWithValue(origVal))
				}
			}
		}
	}

	return strings.Join(parts, " AND "), args, nil
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
