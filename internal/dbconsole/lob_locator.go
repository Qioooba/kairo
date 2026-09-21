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
			SELECT owner, 1 AS prio FROM all_tables WHERE (table_name = :1 OR UPPER(table_name) = :1)
			UNION ALL
			SELECT owner, 2 AS prio FROM all_views WHERE (view_name = :1 OR UPPER(view_name) = :1)
		)
		ORDER BY CASE WHEN owner = :2 THEN 0 ELSE prio END, owner`
		rows, qerr := db.QueryContext(ctx, query, tUpper, uUpper)
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
WHERE (c.owner = :1 OR :1 = '') AND (c.table_name = :2 OR UPPER(c.table_name) = :2)
  AND c.constraint_type = 'U' AND c.status = 'ENABLED'
ORDER BY c.constraint_name, cc.position`
		rows, err := db.QueryContext(ctx, query, owner, table)
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
WHERE (i.table_owner = :1 OR :1 = '') AND (i.table_name = :2 OR UPPER(i.table_name) = :2)
  AND i.uniqueness = 'UNIQUE'
ORDER BY i.index_name, ic.column_position`
		idxRows, err := db.QueryContext(ctx, idxQuery, owner, table)
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

// ResolveRowIDByKeys 当表无显式主键且客户端未传 ROWID 时，通过行特征键动态回查 Oracle ROWID。
func (m *Manager) ResolveRowIDByKeys(ctx context.Context, source Source, owner, table string, keys map[string]any, fields []Field) (string, error) {
	if source.Kind != KindOracle || len(keys) == 0 || strings.TrimSpace(table) == "" {
		return "", errors.New("仅支持 Oracle 且需要有效 keys")
	}
	owner = strings.ToUpper(strings.TrimSpace(owner))
	table = strings.ToUpper(strings.TrimSpace(table))

	type candidate struct {
		name     string
		priority int
		val      any
	}

	fieldMap := make(map[string]Field)
	for _, f := range fields {
		fieldMap[strings.ToUpper(f.Name)] = f
	}

	var candidates []candidate
	for k, v := range keys {
		if v == nil || v == "" {
			continue
		}
		kUpper := strings.ToUpper(k)
		f, hasField := fieldMap[kUpper]
		if hasField {
			if isLOBType(f.DataType) || f.DataType == "LONG" || f.DataType == "XMLTYPE" {
				continue
			}
		}

		prio := 10
		// 优先识别常见 ID、编号、编码列
		if strings.EqualFold(kUpper, "SEQNO") || strings.EqualFold(kUpper, "ID") || strings.HasSuffix(kUpper, "_ID") || strings.HasSuffix(kUpper, "ID") {
			prio = 1
		} else if strings.HasSuffix(kUpper, "_NO") || strings.HasSuffix(kUpper, "NO") || strings.HasSuffix(kUpper, "_SEQ") {
			prio = 2
		} else if strings.HasSuffix(kUpper, "_CODE") || strings.HasSuffix(kUpper, "CODE") {
			prio = 3
		} else if hasField && (strings.Contains(f.DataType, "VARCHAR") || strings.Contains(f.DataType, "CHAR") || strings.Contains(f.DataType, "NUMBER")) {
			prio = 4
		} else if hasField && (strings.Contains(f.DataType, "DATE") || strings.Contains(f.DataType, "TIME")) {
			prio = 8 // 日期列可能受 NLS 格式影响，降权
		}

		// 忽略超长文本作为定位条件
		if s, ok := v.(string); ok && len(s) > 512 {
			continue
		}

		candidates = append(candidates, candidate{name: kUpper, priority: prio, val: v})
	}

	if len(candidates) == 0 {
		return "", errors.New("缺少可用于定位 ROWID 的有效键值")
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		return candidates[i].name < candidates[j].name
	})

	// 选取前 6 个最高优先级的特征键，防止构造过于冗长的 SQL
	if len(candidates) > 6 {
		candidates = candidates[:6]
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

	whereParts := make([]string, 0, len(candidates))
	args := make([]any, 0, len(candidates))
	for i, c := range candidates {
		colQ, cerr := quoteGridIdentifier(KindOracle, c.name, "列名")
		if cerr != nil {
			return "", cerr
		}
		paramName := "kairo_rid" + strconv.Itoa(i+1)
		whereParts = append(whereParts, colQ+" = :"+paramName)
		args = append(args, sql.Named(paramName, c.val))
	}

	querySQL := fmt.Sprintf("SELECT ROWIDTOCHAR(ROWID) FROM %s%s WHERE %s AND ROWNUM <= 1", ownerSQL, tableSQL, strings.Join(whereParts, " AND "))

	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var rowID string
	qerr := m.withSQL(queryCtx, source, func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, querySQL, args...).Scan(&rowID)
	})
	if qerr != nil || strings.TrimSpace(rowID) == "" {
		return "", qerr
	}
	return strings.TrimSpace(rowID), nil
}
