package dbconsole

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

func metadataIdent(name string) error {
	if name == "" || len(name) > 256 {
		return errors.New("schema/object 不能为空且不能超过 256 字节")
	}
	return nil
}

func (m *Manager) InspectObject(ctx context.Context, source Source, schema, object, objectType string) (ObjectInspect, error) {
	out := ObjectInspect{}
	fields, err := m.Fields(ctx, source, schema, object)
	if err != nil {
		return out, err
	}
	out.Fields = fields
	indexes, err := m.Indexes(ctx, source, schema, object)
	if err != nil {
		return out, err
	}
	out.Indexes = indexes
	constraints, err := m.Constraints(ctx, source, schema, object)
	if err != nil {
		return out, err
	}
	out.Constraints = constraints
	ddl, sourceKind, ddlErr := m.objectDDL(ctx, source, schema, object, objectType)
	out.DDL = ddl
	out.DDLSource = sourceKind
	if ddlErr != nil {
		out.DDLError = ddlErr.Error()
	}
	text, _ := m.objectSourceText(ctx, source, schema, object, objectType)
	out.SourceText = text
	return out, nil
}

func (m *Manager) Indexes(ctx context.Context, source Source, schema, object string) ([]IndexInfo, error) {
	if err := metadataIdent(schema); err != nil {
		return nil, err
	}
	if err := metadataIdent(object); err != nil {
		return nil, err
	}
	cacheKey := fmt.Sprintf("%s\x00indexes\x00%s\x00%s", source.ID, strings.ToUpper(schema), strings.ToUpper(object))
	if cached, ok := metadataCacheGet[[]IndexInfo](m, cacheKey); ok {
		return append([]IndexInfo(nil), cached...), nil
	}
	var out []IndexInfo
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var rows *sql.Rows
		var queryErr error
		if source.Kind == KindOracle {
			rows, queryErr = db.QueryContext(ctx, `SELECT i.index_name, i.index_type, i.uniqueness, c.column_name
FROM all_indexes i
JOIN all_ind_columns c ON i.owner = c.index_owner AND i.index_name = c.index_name
WHERE i.table_owner = :1 AND i.table_name = :2
ORDER BY i.index_name, c.column_position`, strings.ToUpper(schema), strings.ToUpper(object))
		} else {
			rows, queryErr = db.QueryContext(ctx, `SELECT index_name, index_type, non_unique, column_name
FROM information_schema.statistics
WHERE table_schema = ? AND table_name = ?
ORDER BY index_name, seq_in_index`, schema, object)
		}
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		byName := map[string]*IndexInfo{}
		order := make([]string, 0)
		for rows.Next() {
			var name, idxType, uniqueness, column string
			if source.Kind == KindOracle {
				if err := rows.Scan(&name, &idxType, &uniqueness, &column); err != nil {
					return err
				}
			} else {
				var nonUnique int
				if err := rows.Scan(&name, &idxType, &nonUnique, &column); err != nil {
					return err
				}
				if nonUnique == 0 {
					uniqueness = "UNIQUE"
				} else {
					uniqueness = "NONUNIQUE"
				}
			}
			item := byName[name]
			if item == nil {
				item = &IndexInfo{Name: name, Type: idxType, Uniqueness: uniqueness}
				byName[name] = item
				order = append(order, name)
			}
			if column != "" {
				item.Columns = append(item.Columns, column)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		out = make([]IndexInfo, 0, len(order))
		for _, name := range order {
			out = append(out, *byName[name])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	metadataCacheSet(m, cacheKey, append([]IndexInfo(nil), out...))
	return out, nil
}

func (m *Manager) Constraints(ctx context.Context, source Source, schema, object string) ([]ConstraintInfo, error) {
	if err := metadataIdent(schema); err != nil {
		return nil, err
	}
	if err := metadataIdent(object); err != nil {
		return nil, err
	}
	cacheKey := fmt.Sprintf("%s\x00constraints\x00%s\x00%s", source.ID, strings.ToUpper(schema), strings.ToUpper(object))
	if cached, ok := metadataCacheGet[[]ConstraintInfo](m, cacheKey); ok {
		return append([]ConstraintInfo(nil), cached...), nil
	}
	var out []ConstraintInfo
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var rows *sql.Rows
		var queryErr error
		if source.Kind == KindOracle {
			rows, queryErr = db.QueryContext(ctx, `SELECT c.constraint_name, c.constraint_type, NVL(cc.column_name, ''),
       NVL(NVL2(c.r_constraint_name, c.r_owner || '.' || c.r_constraint_name, ''), '')
FROM all_constraints c
LEFT JOIN all_cons_columns cc ON c.owner = cc.owner AND c.constraint_name = cc.constraint_name
WHERE c.owner = :1 AND c.table_name = :2
ORDER BY DECODE(c.constraint_type,'P',1,'U',2,'R',3,'C',4,5), c.constraint_name, cc.position`, strings.ToUpper(schema), strings.ToUpper(object))
		} else {
			rows, queryErr = db.QueryContext(ctx, `SELECT tc.constraint_name, tc.constraint_type,
       GROUP_CONCAT(kcu.column_name ORDER BY kcu.ordinal_position SEPARATOR ', '),
       CONCAT_WS(' ', tc.constraint_type, kcu.referenced_table_name)
FROM information_schema.table_constraints tc
LEFT JOIN information_schema.key_column_usage kcu
  ON tc.constraint_schema = kcu.constraint_schema AND tc.constraint_name = kcu.constraint_name AND tc.table_name = kcu.table_name
WHERE tc.table_schema = ? AND tc.table_name = ?
GROUP BY tc.constraint_name, tc.constraint_type, kcu.referenced_table_name
ORDER BY tc.constraint_type, tc.constraint_name`, schema, object)
		}
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		if source.Kind == KindOracle {
			byName := map[string]*ConstraintInfo{}
			order := make([]string, 0)
			for rows.Next() {
				var name, ctype string
				var column, detail sql.NullString
				if err := rows.Scan(&name, &ctype, &column, &detail); err != nil {
					return err
				}
				item := byName[name]
				if item == nil {
					item = &ConstraintInfo{Name: name, Type: constraintLabel(ctype), Detail: detail.String}
					byName[name] = item
					order = append(order, name)
				}
				if column.String != "" {
					if item.Columns != "" {
						item.Columns += ", "
					}
					item.Columns += column.String
				}
			}
			if err := rows.Err(); err != nil {
				return err
			}
			out = make([]ConstraintInfo, 0, len(order))
			for _, name := range order {
				out = append(out, *byName[name])
			}
			return nil
		}
		for rows.Next() {
			var item ConstraintInfo
			var cols, detail sql.NullString
			if err := rows.Scan(&item.Name, &item.Type, &cols, &detail); err != nil {
				return err
			}
			item.Columns = cols.String
			item.Detail = detail.String
			item.Type = constraintLabel(item.Type)
			out = append(out, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	metadataCacheSet(m, cacheKey, append([]ConstraintInfo(nil), out...))
	return out, nil
}

func constraintLabel(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "P", "PRIMARY KEY":
		return "PRIMARY KEY"
	case "U", "UNIQUE":
		return "UNIQUE"
	case "R", "FOREIGN KEY":
		return "FOREIGN KEY"
	case "C", "CHECK":
		return "CHECK"
	default:
		return strings.ToUpper(code)
	}
}

func (m *Manager) markPrimaryKeys(ctx context.Context, db *sql.DB, source Source, schema, object string, fields []Field) error {
	names := map[string]struct{}{}
	var rows *sql.Rows
	var err error
	if source.Kind == KindOracle {
		rows, err = db.QueryContext(ctx, `SELECT cc.column_name
FROM all_constraints c
JOIN all_cons_columns cc ON c.owner = cc.owner AND c.constraint_name = cc.constraint_name
WHERE c.constraint_type = 'P' AND c.owner = :1 AND c.table_name = :2`, strings.ToUpper(schema), strings.ToUpper(object))
	} else {
		rows, err = db.QueryContext(ctx, `SELECT column_name
FROM information_schema.key_column_usage
WHERE table_schema = ? AND table_name = ? AND constraint_name = 'PRIMARY'`, schema, object)
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		names[strings.ToUpper(name)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	for i := range fields {
		if _, ok := names[strings.ToUpper(fields[i].Name)]; ok {
			fields[i].PrimaryKey = true
		}
	}
	return nil
}

func (m *Manager) objectDDL(ctx context.Context, source Source, schema, object, objectType string) (string, string, error) {
	ddlType := ddlObjectType(objectType)
	var text string
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		if source.Kind == KindOracle {
			var raw sql.NullString
			queryErr := db.QueryRowContext(ctx, `SELECT DBMS_METADATA.GET_DDL(:1, :2, :3) FROM dual`, ddlType, strings.ToUpper(object), strings.ToUpper(schema)).Scan(&raw)
			if queryErr != nil {
				fallback, ferr := dictionaryDDL(ctx, db, source, schema, object, objectType)
				if ferr != nil {
					return queryErr
				}
				text = fallback
				return nil
			}
			text = strings.TrimSpace(raw.String)
			return nil
		}
		var raw sql.NullString
		queryErr := db.QueryRowContext(ctx, `SHOW CREATE TABLE `+mysqlQualified(schema, object)).Scan(new(string), &raw)
		if queryErr != nil {
			fallback, ferr := dictionaryDDL(ctx, db, source, schema, object, objectType)
			if ferr != nil {
				return queryErr
			}
			text = fallback
			return nil
		}
		text = raw.String
		return nil
	})
	if err != nil {
		fallback, ferr := m.dictionaryDDLStandalone(ctx, source, schema, object, objectType)
		if ferr != nil {
			return "", "", err
		}
		return fallback, "dictionary", nil
	}
	if text == "" {
		return "", "", errors.New("对象没有可显示的 DDL")
	}
	sourceKind := "dbms_metadata"
	if source.Kind == KindMySQL {
		sourceKind = "show_create"
	}
	return text, sourceKind, nil
}

func (m *Manager) dictionaryDDLStandalone(ctx context.Context, source Source, schema, object, objectType string) (string, error) {
	var text string
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		ddl, err := dictionaryDDL(ctx, db, source, schema, object, objectType)
		text = ddl
		return err
	})
	return text, err
}

func dictionaryDDL(ctx context.Context, db *sql.DB, source Source, schema, object, objectType string) (string, error) {
	fields, err := loadFieldRows(ctx, db, source, schema, object)
	if err != nil {
		return "", err
	}
	if len(fields) == 0 {
		return "-- 无字段信息，无法从数据字典重建 DDL", nil
	}
	var b strings.Builder
	b.WriteString("-- reconstructed from data dictionary\n")
	b.WriteString("CREATE TABLE " + quoteIdent(source.Kind, schema) + "." + quoteIdent(source.Kind, object) + " (\n")
	for i, field := range fields {
		b.WriteString("  " + quoteIdent(source.Kind, field.Name) + " " + field.Definition)
		if !field.Nullable {
			b.WriteString(" NOT NULL")
		}
		if i < len(fields)-1 {
			b.WriteString(",")
		}
		b.WriteByte('\n')
	}
	b.WriteString(");")
	_ = objectType
	return b.String(), nil
}

func loadFieldRows(ctx context.Context, db *sql.DB, source Source, schema, object string) ([]Field, error) {
	var rows *sql.Rows
	var err error
	if source.Kind == KindOracle {
		rows, err = db.QueryContext(ctx, `SELECT column_name, data_type, nullable, column_id,
CASE WHEN data_type IN ('VARCHAR2','CHAR','NVARCHAR2','NCHAR','RAW') THEN data_type || '(' || data_length || ')'
     WHEN data_type = 'NUMBER' AND data_precision IS NOT NULL THEN data_type || '(' || data_precision || ',' || NVL(data_scale,0) || ')'
     ELSE data_type END
FROM all_tab_columns WHERE owner = :1 AND table_name = :2 ORDER BY column_id`, strings.ToUpper(schema), strings.ToUpper(object))
	} else {
		rows, err = db.QueryContext(ctx, `SELECT column_name, data_type, is_nullable, ordinal_position, column_type
FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`, schema, object)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Field
	for rows.Next() {
		var item Field
		var nullable string
		if err := rows.Scan(&item.Name, &item.DataType, &nullable, &item.Ordinal, &item.Definition); err != nil {
			return nil, err
		}
		item.Nullable = strings.EqualFold(nullable, "yes") || strings.EqualFold(nullable, "y")
		out = append(out, item)
	}
	return out, rows.Err()
}

func (m *Manager) objectSourceText(ctx context.Context, source Source, schema, object, objectType string) (string, error) {
	upper := strings.ToUpper(objectType)
	var text string
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		if source.Kind == KindOracle {
			switch upper {
			case "VIEW", "MATERIALIZED VIEW":
				return db.QueryRowContext(ctx, `SELECT text FROM all_views WHERE owner = :1 AND view_name = :2`, strings.ToUpper(schema), strings.ToUpper(object)).Scan(&text)
			case "FUNCTION", "PROCEDURE", "PACKAGE", "TRIGGER":
				rows, err := db.QueryContext(ctx, `SELECT text FROM all_source WHERE owner = :1 AND name = :2 AND type = :3 ORDER BY line`, strings.ToUpper(schema), strings.ToUpper(object), upper)
				if err != nil {
					return err
				}
				defer rows.Close()
				var b strings.Builder
				for rows.Next() {
					var line sql.NullString
					if err := rows.Scan(&line); err != nil {
						return err
					}
					b.WriteString(line.String)
				}
				text = b.String()
				return rows.Err()
			}
			return nil
		}
		if upper == "VIEW" {
			return db.QueryRowContext(ctx, `SELECT view_definition FROM information_schema.views WHERE table_schema = ? AND table_name = ?`, schema, object).Scan(&text)
		}
		if upper == "FUNCTION" || upper == "PROCEDURE" {
			return db.QueryRowContext(ctx, `SELECT routine_definition FROM information_schema.routines WHERE routine_schema = ? AND routine_name = ?`, schema, object).Scan(&text)
		}
		return nil
	})
	return strings.TrimSpace(text), err
}

func ddlObjectType(objectType string) string {
	switch strings.ToUpper(strings.TrimSpace(objectType)) {
	case "VIEW":
		return "VIEW"
	case "MATERIALIZED VIEW":
		return "MATERIALIZED_VIEW"
	case "FUNCTION":
		return "FUNCTION"
	case "PROCEDURE":
		return "PROCEDURE"
	case "PACKAGE":
		return "PACKAGE"
	case "TRIGGER":
		return "TRIGGER"
	default:
		return "TABLE"
	}
}

func quoteIdent(kind, name string) string {
	q := `"`
	if kind == KindMySQL {
		q = "`"
	}
	return q + strings.ReplaceAll(name, q, q+q) + q
}

func mysqlQualified(schema, object string) string {
	return quoteIdent(KindMySQL, schema) + "." + quoteIdent(KindMySQL, object)
}

func (m *Manager) Explain(ctx context.Context, source Source, query string) ([]ExplainRow, error) {
	if err := ValidateReadOnlySQL(source.Kind, query); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	var rowsOut []ExplainRow
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		if source.Kind == KindMySQL {
			rows, err := db.QueryContext(ctx, "EXPLAIN "+query)
			if err != nil {
				return err
			}
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil {
				return err
			}
			for rows.Next() {
				raw := make([]sql.NullString, len(cols))
				dest := make([]any, len(cols))
				for i := range raw {
					dest[i] = &raw[i]
				}
				if err := rows.Scan(dest...); err != nil {
					return err
				}
				item := ExplainRow{}
				for i, col := range cols {
					value := raw[i].String
					switch strings.ToLower(col) {
					case "id":
						item.ID = value
					case "select_type", "type":
						if item.Operation == "" {
							item.Operation = value
						} else {
							item.Options = value
						}
					case "table":
						item.Object = value
					case "rows":
						item.Cardinality = value
					case "extra":
						item.Extra = value
					case "possible_keys", "key":
						if item.Extra != "" {
							item.Extra += "; "
						}
						item.Extra += col + "=" + value
					}
					if item.Raw != "" {
						item.Raw += " | "
					}
					item.Raw += col + "=" + value
				}
				rowsOut = append(rowsOut, item)
			}
			return rows.Err()
		}
		id := "KAIRO" + randomExplainID()
		// EXPLAIN PLAN writes PLAN_TABLE. This is a backend-owned diagnostic, not user DML.
		if _, err := db.ExecContext(ctx, "EXPLAIN PLAN SET STATEMENT_ID = '"+id+"' FOR "+query); err != nil {
			return fmt.Errorf("Oracle 执行计划失败（需要 PLAN_TABLE 写权限）: %w", err)
		}
		rows, err := db.QueryContext(ctx, `SELECT PLAN_TABLE_OUTPUT FROM TABLE(DBMS_XPLAN.DISPLAY(NULL, :1, 'TYPICAL'))`, id)
		if err != nil {
			_, _ = db.ExecContext(ctx, `DELETE FROM plan_table WHERE statement_id = :1`, id)
			return err
		}
		defer rows.Close()
		var rawLines []string
		for rows.Next() {
			var line sql.NullString
			if err := rows.Scan(&line); err != nil {
				return err
			}
			rawLines = append(rawLines, line.String)
			rowsOut = append(rowsOut, parseOraclePlanLine(line.String))
		}
		_ = rawLines
		if err := rows.Err(); err != nil {
			return err
		}
		_, _ = db.ExecContext(ctx, `DELETE FROM plan_table WHERE statement_id = :1`, id)
		return nil
	})
	return rowsOut, err
}

func parseOraclePlanLine(line string) ExplainRow {
	trimmed := strings.TrimSpace(line)
	item := ExplainRow{Raw: line}
	if strings.Contains(trimmed, "|") {
		parts := strings.Split(trimmed, "|")
		if len(parts) >= 4 {
			item.ID = strings.TrimSpace(parts[1])
			item.Operation = strings.TrimSpace(parts[2])
			if len(parts) > 4 {
				item.Object = strings.TrimSpace(parts[3])
			}
			if len(parts) > 6 {
				item.Cost = strings.TrimSpace(parts[5])
				item.Cardinality = strings.TrimSpace(parts[6])
			}
		}
		return item
	}
	item.Operation = trimmed
	return item
}

func randomExplainID() string {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", 1)
	}
	return strings.ToUpper(hex.EncodeToString(raw))
}
