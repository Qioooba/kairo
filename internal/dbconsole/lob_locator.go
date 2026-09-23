package dbconsole

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FindTableOwner 在 Oracle 中查找某表归属的真实 Owner（优先匹配当前用户）。
func (m *Manager) FindTableOwner(ctx context.Context, source Source, table string) (string, error) {
	if source.Kind != KindOracle || strings.TrimSpace(table) == "" {
		return "", nil
	}
	tUpper := strings.ToUpper(strings.TrimSpace(table))
	uUpper := strings.ToUpper(strings.TrimSpace(source.Username))
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var owner string
	err := m.withSQL(queryCtx, source, func(ctx context.Context, db *sql.DB) error {
		query := `SELECT owner FROM (
			SELECT owner, 1 AS prio FROM all_tables WHERE (table_name = :1 OR UPPER(table_name) = :2)
			UNION ALL
			SELECT owner, 2 AS prio FROM all_views WHERE (view_name = :3 OR UPPER(view_name) = :4)
		)
		ORDER BY CASE WHEN owner = :5 THEN 0 ELSE prio END, owner`
		rows, qerr := db.QueryContext(ctx, query, tUpper, tUpper, tUpper, tUpper, uUpper)
		if qerr != nil {
			return qerr
		}
		defer rows.Close()
		if rows.Next() {
			return rows.Scan(&owner)
		}
		return rows.Err()
	})
	if err != nil || owner == "" {
		return "", err
	}
	return strings.ToUpper(strings.TrimSpace(owner)), nil
}

// FindUniqueKeyColumns 查找表上是否存在可用唯一约束或唯一索引，且其所有列均存在于 keys 中。
func (m *Manager) FindUniqueKeyColumns(ctx context.Context, source Source, owner, table string, keys map[string]any) []string {
	if source.Kind != KindOracle || len(keys) == 0 || strings.TrimSpace(table) == "" {
		return nil
	}
	owner = strings.ToUpper(strings.TrimSpace(owner))
	table = strings.ToUpper(strings.TrimSpace(table))

	hasKey := func(col string) bool {
		for k, v := range keys {
			if strings.EqualFold(k, col) && v != nil && v != "" {
				return true
			}
		}
		return false
	}

	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var foundCols []string
	_ = m.withSQL(queryCtx, source, func(ctx context.Context, db *sql.DB) error {
		// 1. 唯一约束 (constraint_type = 'U')
		query := `SELECT c.constraint_name, cc.column_name
FROM all_constraints c
JOIN all_cons_columns cc ON c.owner = cc.owner AND c.constraint_name = cc.constraint_name AND c.table_name = cc.table_name
WHERE (c.owner = :1 OR :2 = '') AND (c.table_name = :3 OR UPPER(c.table_name) = :4)
  AND c.constraint_type = 'U' AND c.status = 'ENABLED'
ORDER BY c.constraint_name, cc.position`
		rows, err := db.QueryContext(ctx, query, owner, owner, table, table)
		if err == nil {
			defer rows.Close()
			byConstraint := make(map[string][]string)
			var cName, colName string
			for rows.Next() {
				if rows.Scan(&cName, &colName) == nil {
					byConstraint[cName] = append(byConstraint[cName], colName)
				}
			}
			for _, cols := range byConstraint {
				allMatch := true
				for _, c := range cols {
					if !hasKey(c) {
						allMatch = false
						break
					}
				}
				if allMatch && len(cols) > 0 {
					foundCols = cols
					return nil
				}
			}
		}

		// 2. 唯一索引 (uniqueness = 'UNIQUE')
		idxQuery := `SELECT ic.index_name, ic.column_name
FROM all_indexes i
JOIN all_ind_columns ic ON i.owner = ic.index_owner AND i.index_name = ic.index_name AND i.table_name = ic.table_name
WHERE (i.table_owner = :1 OR :2 = '') AND (i.table_name = :3 OR UPPER(i.table_name) = :4)
  AND i.uniqueness = 'UNIQUE'
ORDER BY i.index_name, ic.column_position`
		idxRows, err := db.QueryContext(ctx, idxQuery, owner, owner, table, table)
		if err == nil {
			defer idxRows.Close()
			byIndex := make(map[string][]string)
			var idxName, colName string
			for idxRows.Next() {
				if idxRows.Scan(&idxName, &colName) == nil {
					byIndex[idxName] = append(byIndex[idxName], colName)
				}
			}
			for _, cols := range byIndex {
				allMatch := true
				for _, c := range cols {
					if !hasKey(c) {
						allMatch = false
						break
					}
				}
				if allMatch && len(cols) > 0 {
					foundCols = cols
					return nil
				}
			}
		}
		return nil
	})
	return foundCols
}

// ErrRowLocatorNotUnique 表示行特征回查匹配到多行，无法唯一定位。
// 调用方必须把它当作“请重新查询”而不是“取第一行”。
var ErrRowLocatorNotUnique = errors.New("匹配到多行，无法唯一定位该行，请重新查询后重试")

// gridLocatorComparable 判断某列是否可以作为受控的可比较快照参与定位比较（DB-07）。
// LOB / LONG / XMLTYPE 等无法用 = 比较的类型必须排除。
func gridLocatorComparable(field Field) bool {
	dataType := strings.ToUpper(strings.TrimSpace(field.DataType))
	if isLOBType(dataType) || dataType == "LONG" || dataType == "LONG RAW" || dataType == "XMLTYPE" {
		return false
	}
	return true
}

// ResolveRowIDByKeys 当表无显式主键且客户端未传 ROWID 时，通过行特征键动态回查 Oracle ROWID。
//
// DB-07 的硬性约束：
//   - 不把“前六个非 NULL 列”当成唯一键：必须使用元数据确认的完整可比较快照，
//     缺少任何可比较列都拒绝（否则两行可能只在未比较的列上不同）；
//   - NULL 以 IS NULL 参与比较；
//   - 最多取 2 行并显式区分 0 / 1 / 多行；多行返回“无法唯一定位”，绝不静默取第一行；
//   - 若查询属于未提交事务（sessionID 非空），定位必须走同一事务快照。
func (m *Manager) ResolveRowIDByKeys(ctx context.Context, source Source, owner, table string, keys map[string]any, fields []Field, sessionID string) (string, error) {
	if source.Kind != KindOracle || len(keys) == 0 || strings.TrimSpace(table) == "" {
		return "", errors.New("仅支持 Oracle 且需要有效 keys")
	}
	owner = strings.ToUpper(strings.TrimSpace(owner))
	table = strings.ToUpper(strings.TrimSpace(table))
	if len(fields) == 0 {
		return "", fmt.Errorf("缺少目标表 %s 的字段元数据，无法确认完整可比较快照，请重新查询", table)
	}

	fieldMap := make(map[string]Field, len(fields))
	comparable := make([]string, 0, len(fields))
	for _, f := range fields {
		fieldMap[strings.ToUpper(f.Name)] = f
		if gridLocatorComparable(f) {
			comparable = append(comparable, f.Name)
		}
	}
	if len(comparable) == 0 {
		return "", fmt.Errorf("目标表 %s 没有可用于定位的可比较列，请重新查询", table)
	}

	// 只接受服务器已知的物理列；未知键直接拒绝，避免把客户端自由文本拼进 WHERE。
	whereParts := make([]string, 0, len(comparable))
	args := make([]any, 0, len(comparable))
	matched := make(map[string]bool, len(comparable))
	names := make([]string, 0, len(comparable))
	for _, col := range comparable {
		names = append(names, col)
	}
	sort.Strings(names)
	for _, col := range names {
		value, found := findMapValueInsensitive(keys, col)
		if !found {
			continue
		}
		quoted, qErr := quoteGridIdentifier(KindOracle, col, "列名")
		if qErr != nil {
			return "", qErr
		}
		if value == nil {
			whereParts = append(whereParts, quoted+" IS NULL")
			matched[strings.ToUpper(col)] = true
			continue
		}
		paramName := "kairo_rid" + strconv.Itoa(len(args)+1)
		whereParts = append(whereParts, quoted+" = :"+paramName)
		field := fieldMap[strings.ToUpper(col)]
		args = append(args, sql.Named(paramName, normalizeTypedParam(KindOracle, field.DataType, value)))
		matched[strings.ToUpper(col)] = true
	}
	for _, col := range comparable {
		if !matched[strings.ToUpper(col)] {
			return "", fmt.Errorf("行快照不完整（缺少可比较列 %s），无法唯一定位该行，请重新查询", col)
		}
	}
	if len(whereParts) == 0 {
		return "", errors.New("缺少可用于定位 ROWID 的有效键值")
	}

	tableSQL, err := quoteGridIdentifier(KindOracle, table, "表名")
	if err != nil {
		return "", err
	}
	ownerSQL := ""
	if owner != "" {
		oq, oerr := quoteGridIdentifier(KindOracle, owner, "schema")
		if oerr != nil {
			return "", oerr
		}
		ownerSQL = oq + "."
	}

	// 最多取 2 行：0 行 -> 未找到；2 行 -> 无法唯一定位（DB-07）。
	querySQL := fmt.Sprintf("SELECT ROWIDTOCHAR(ROWID) FROM %s%s WHERE %s AND ROWNUM <= 2", ownerSQL, tableSQL, strings.Join(whereParts, " AND "))

	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var resolved string
	runQuery := func(ctx context.Context, q lobQueryer) error {
		rows, qerr := q.QueryContext(ctx, querySQL, args...)
		if qerr != nil {
			return qerr
		}
		defer rows.Close()
		found := make([]string, 0, 2)
		for rows.Next() {
			var rowID string
			if scanErr := rows.Scan(&rowID); scanErr != nil {
				return scanErr
			}
			if strings.TrimSpace(rowID) != "" {
				found = append(found, strings.TrimSpace(rowID))
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		switch len(found) {
		case 0:
			return errors.New("未找到匹配行，请重新查询后再操作")
		case 1:
			resolved = found[0]
			return nil
		default:
			return ErrRowLocatorNotUnique
		}
	}

	// 有未提交事务时必须在同一事务快照里定位，否则看不到本会话新增/修改的日志（DB-07 第 4 条）。
	if strings.TrimSpace(sessionID) != "" {
		entry, entryErr := m.transactionForContext(queryCtx, source, sessionID, false)
		if entryErr != nil {
			return "", entryErr
		}
		if entry == nil {
			return "", errors.New("原查询事务已结束，请重新查询后再加载 LOB")
		}
		entry.mu.Lock()
		defer entry.mu.Unlock()
		if err := runQuery(queryCtx, entry.tx); err != nil {
			return "", err
		}
		return resolved, nil
	}

	err = m.withSQL(queryCtx, source, func(ctx context.Context, db *sql.DB) error {
		return runQuery(ctx, db)
	})
	if err != nil {
		return "", err
	}
	return resolved, nil
}
