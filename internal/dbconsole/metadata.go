package dbconsole

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	metadataCacheTTL        = 5 * time.Minute
	metadataCacheMaxEntries = 512
)

type metadataCacheEntry struct {
	expires time.Time
	value   any
}

func metadataCacheGet[T any](m *Manager, key string) (T, bool) {
	var zero T
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.metadataCache[key]
	if !ok || time.Now().After(entry.expires) {
		delete(m.metadataCache, key)
		return zero, false
	}
	value, ok := entry.value.(T)
	return value, ok
}

func metadataCacheSet(m *Manager, key string, value any) {
	m.mu.Lock()
	if len(m.metadataCache) >= metadataCacheMaxEntries {
		now := time.Now()
		oldestKey := ""
		var oldest time.Time
		for candidate, entry := range m.metadataCache {
			if now.After(entry.expires) {
				delete(m.metadataCache, candidate)
				continue
			}
			if oldestKey == "" || entry.expires.Before(oldest) {
				oldestKey, oldest = candidate, entry.expires
			}
		}
		if len(m.metadataCache) >= metadataCacheMaxEntries && oldestKey != "" {
			delete(m.metadataCache, oldestKey)
		}
	}
	m.metadataCache[key] = metadataCacheEntry{expires: time.Now().Add(metadataCacheTTL), value: value}
	m.mu.Unlock()
}

func (m *Manager) invalidateMetadataLocked(sourceID string) {
	prefix := sourceID + "\x00"
	for key := range m.metadataCache {
		if strings.HasPrefix(key, prefix) {
			delete(m.metadataCache, key)
		}
	}
}

func (m *Manager) InvalidateMetadata(sourceID string) {
	m.mu.Lock()
	m.invalidateMetadataLocked(sourceID)
	m.mu.Unlock()
}

type Schema struct {
	Name string `json:"name"`
}

type Object struct {
	Schema   string `json:"schema"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Category string `json:"category"`
}

type Field struct {
	Name       string `json:"name"`
	DataType   string `json:"data_type"`
	Nullable   bool   `json:"nullable"`
	Ordinal    int    `json:"ordinal"`
	Definition string `json:"definition,omitempty"`
	PrimaryKey bool   `json:"primary_key,omitempty"`
	Default    string `json:"default,omitempty"`
	Comment    string `json:"comment,omitempty"`
}

type IndexInfo struct {
	Name       string   `json:"name"`
	Type       string   `json:"type,omitempty"`
	Uniqueness string   `json:"uniqueness,omitempty"`
	Columns    []string `json:"columns"`
}

type ConstraintInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Columns string `json:"columns,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

type ObjectInspect struct {
	Fields      []Field          `json:"fields"`
	Indexes     []IndexInfo      `json:"indexes"`
	Constraints []ConstraintInfo `json:"constraints"`
	DDL         string           `json:"ddl,omitempty"`
	DDLSource   string           `json:"ddl_source,omitempty"`
	DDLError    string           `json:"ddl_error,omitempty"`
	SourceText  string           `json:"source_text,omitempty"`
}

type ExplainRow struct {
	ID          string            `json:"id,omitempty"`
	Operation   string            `json:"operation,omitempty"`
	Object      string            `json:"object,omitempty"`
	Options     string            `json:"options,omitempty"`
	Cardinality string            `json:"cardinality,omitempty"`
	Cost        string            `json:"cost,omitempty"`
	Extra       string            `json:"extra,omitempty"`
	Raw         string            `json:"raw,omitempty"`
	ColumnOrder []string          `json:"column_order,omitempty"`
	Cells       map[string]string `json:"cells,omitempty"`
}

func (m *Manager) Schemas(ctx context.Context, source Source) ([]Schema, error) {
	if source.Kind == KindRedis {
		return nil, errors.New("Redis 没有 schema")
	}
	cacheKey := source.ID + "\x00schemas"
	if cached, ok := metadataCacheGet[[]Schema](m, cacheKey); ok {
		return append([]Schema(nil), cached...), nil
	}
	var out []Schema
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		out = out[:0]
		query := "SELECT schema_name FROM information_schema.schemata ORDER BY schema_name"
		if source.Kind == KindOracle {
			query = "SELECT username FROM all_users ORDER BY username"
		}
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() && len(out) < 1000 {
			var item Schema
			if err := rows.Scan(&item.Name); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	metadataCacheSet(m, cacheKey, append([]Schema(nil), out...))
	return out, nil
}

func (m *Manager) Objects(ctx context.Context, source Source, schema, search, category string) ([]Object, error) {
	if source.Kind == KindRedis {
		return nil, errors.New("Redis 没有表对象")
	}
	if schema == "" || len(schema) > 256 || len(search) > 256 {
		return nil, errors.New("schema 不能为空，且 schema/search 不能超过 256 字节")
	}
	cat := strings.ToLower(strings.TrimSpace(category))
	cacheKey := fmt.Sprintf("%s\x00objects\x00%s\x00%s\x00%s", source.ID, strings.ToUpper(schema), strings.ToUpper(cat), strings.ToUpper(search))
	if cached, ok := metadataCacheGet[[]Object](m, cacheKey); ok {
		return append([]Object(nil), cached...), nil
	}
	var out []Object
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		out = out[:0]
		var rows *sql.Rows
		var err error
		like := "%" + search + "%"
		upperSchema := strings.ToUpper(schema)
		upperSearch := strings.ToUpper(search)
		upperLike := "%" + upperSearch + "%"

		if source.Kind == KindOracle {
			switch cat {
			case "tables":
				rows, err = db.QueryContext(ctx, `SELECT owner, table_name, 'TABLE' FROM (
SELECT owner, table_name FROM all_tables
WHERE owner = :1 AND (:2 = '' OR UPPER(table_name) LIKE :3)
ORDER BY table_name) WHERE ROWNUM <= 1000`, upperSchema, search, upperLike)
			case "views":
				rows, err = db.QueryContext(ctx, `SELECT owner, view_name, 'VIEW' FROM (
SELECT owner, view_name FROM all_views
WHERE owner = :1 AND (:2 = '' OR UPPER(view_name) LIKE :3)
ORDER BY view_name) WHERE ROWNUM <= 1000`, upperSchema, search, upperLike)
			case "functions":
				rows, err = db.QueryContext(ctx, `SELECT owner, object_name, object_type FROM (
SELECT owner, object_name, object_type FROM all_objects
WHERE owner = :1 AND object_type = 'FUNCTION' AND (:2 = '' OR UPPER(object_name) LIKE :3)
ORDER BY object_name) WHERE ROWNUM <= 1000`, upperSchema, search, upperLike)
			case "procedures":
				rows, err = db.QueryContext(ctx, `SELECT owner, object_name, object_type FROM (
SELECT owner, object_name, object_type FROM all_objects
WHERE owner = :1 AND object_type IN ('PROCEDURE','PACKAGE') AND (:2 = '' OR UPPER(object_name) LIKE :3)
ORDER BY object_name) WHERE ROWNUM <= 1000`, upperSchema, search, upperLike)
			case "triggers":
				rows, err = db.QueryContext(ctx, `SELECT owner, trigger_name, 'TRIGGER' FROM (
SELECT owner, trigger_name FROM all_triggers
WHERE owner = :1 AND (:2 = '' OR UPPER(trigger_name) LIKE :3)
ORDER BY trigger_name) WHERE ROWNUM <= 1000`, upperSchema, search, upperLike)
			default:
				rows, err = db.QueryContext(ctx, `SELECT owner, object_name, object_type FROM (
SELECT owner, object_name, object_type FROM all_objects
WHERE owner = :1 AND object_type IN ('TABLE','VIEW','MATERIALIZED VIEW','FUNCTION','PROCEDURE','PACKAGE','SEQUENCE','SYNONYM','TRIGGER') AND (:2 = '' OR UPPER(object_name) LIKE :3)
ORDER BY object_name) WHERE ROWNUM <= 1000`, upperSchema, search, upperLike)
			}
		} else {
			switch cat {
			case "tables":
				rows, err = db.QueryContext(ctx, `SELECT table_schema AS object_schema, table_name AS object_name, 'TABLE' AS object_type
FROM information_schema.tables
WHERE table_schema = ? AND table_type = 'BASE TABLE' AND (? = '' OR table_name LIKE ?)
ORDER BY table_name LIMIT 1000`, schema, search, like)
			case "views":
				rows, err = db.QueryContext(ctx, `SELECT table_schema AS object_schema, table_name AS object_name, 'VIEW' AS object_type
FROM information_schema.tables
WHERE table_schema = ? AND table_type = 'VIEW' AND (? = '' OR table_name LIKE ?)
ORDER BY table_name LIMIT 1000`, schema, search, like)
			case "functions":
				rows, err = db.QueryContext(ctx, `SELECT routine_schema, routine_name, routine_type
FROM information_schema.routines
WHERE routine_schema = ? AND routine_type = 'FUNCTION' AND (? = '' OR routine_name LIKE ?)
ORDER BY routine_name LIMIT 1000`, schema, search, like)
			case "procedures":
				rows, err = db.QueryContext(ctx, `SELECT routine_schema, routine_name, routine_type
FROM information_schema.routines
WHERE routine_schema = ? AND routine_type = 'PROCEDURE' AND (? = '' OR routine_name LIKE ?)
ORDER BY routine_name LIMIT 1000`, schema, search, like)
			case "triggers":
				rows, err = db.QueryContext(ctx, `SELECT trigger_schema, trigger_name, 'TRIGGER'
FROM information_schema.triggers
WHERE trigger_schema = ? AND (? = '' OR trigger_name LIKE ?)
ORDER BY trigger_name LIMIT 1000`, schema, search, like)
			default:
				rows, err = db.QueryContext(ctx, `SELECT object_schema, object_name, object_type FROM (
SELECT table_schema AS object_schema, table_name AS object_name,
       CASE WHEN table_type = 'VIEW' THEN 'VIEW' ELSE 'TABLE' END AS object_type
FROM information_schema.tables WHERE table_schema = ? AND (? = '' OR table_name LIKE ?)
UNION ALL
SELECT routine_schema, routine_name, routine_type
FROM information_schema.routines WHERE routine_schema = ? AND (? = '' OR routine_name LIKE ?)
UNION ALL
SELECT trigger_schema, trigger_name, 'TRIGGER'
FROM information_schema.triggers WHERE trigger_schema = ? AND (? = '' OR trigger_name LIKE ?)
) objects ORDER BY object_type, object_name LIMIT 1000`, schema, search, like, schema, search, like, schema, search, like)
			}
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Object
			if err := rows.Scan(&item.Schema, &item.Name, &item.Type); err != nil {
				return err
			}
			item.Category = objectCategory(item.Type)
			out = append(out, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	metadataCacheSet(m, cacheKey, append([]Object(nil), out...))
	return out, nil
}

func objectCategory(objectType string) string {
	switch strings.ToUpper(strings.TrimSpace(objectType)) {
	case "TABLE", "BASE TABLE":
		return "tables"
	case "VIEW", "MATERIALIZED VIEW":
		return "views"
	case "FUNCTION":
		return "functions"
	case "PROCEDURE", "PACKAGE":
		return "procedures"
	case "TRIGGER":
		return "triggers"
	default:
		return "other"
	}
}

// SetCachedFields stores fields in the metadata cache (useful for mocking and fast lookups).
func (m *Manager) SetCachedFields(sourceID, schema, object string, fields []Field) {
	cacheKey := fmt.Sprintf("%s\x00fields\x00%s\x00%s", sourceID, schema, object)
	metadataCacheSet(m, cacheKey, append([]Field(nil), fields...))
}

func (m *Manager) Fields(ctx context.Context, source Source, schema, object string) ([]Field, error) {
	if source.Kind == KindRedis {
		return nil, errors.New("Redis 没有字段")
	}
	if schema == "" || object == "" || len(schema) > 256 || len(object) > 256 {
		return nil, errors.New("schema/object 不能为空且不能超过 256 字节")
	}
	cacheKey := fmt.Sprintf("%s\x00fields\x00%s\x00%s", source.ID, schema, object)
	if cached, ok := metadataCacheGet[[]Field](m, cacheKey); ok {
		return append([]Field(nil), cached...), nil
	}
	var out []Field
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		out = out[:0]
		var rows *sql.Rows
		var err error
		if source.Kind == KindOracle {
			rows, err = db.QueryContext(ctx, `SELECT c.column_name, c.data_type, c.nullable, c.column_id,
CASE WHEN c.data_type IN ('VARCHAR2','CHAR','NVARCHAR2','NCHAR','RAW') THEN c.data_type || '(' || c.data_length || ')'
     WHEN c.data_type = 'NUMBER' AND c.data_precision IS NOT NULL THEN c.data_type || '(' || c.data_precision || ',' || NVL(c.data_scale,0) || ')'
     ELSE c.data_type END,
c.data_default, NVL(com.comments, '')
FROM all_tab_columns c
LEFT JOIN all_col_comments com ON c.owner = com.owner AND c.table_name = com.table_name AND c.column_name = com.column_name
WHERE c.owner = :1 AND c.table_name = :2 ORDER BY c.column_id`, strings.ToUpper(schema), strings.ToUpper(object))
		} else {
			rows, err = db.QueryContext(ctx, `SELECT column_name, data_type, is_nullable, ordinal_position, column_type, column_default, COALESCE(column_comment, '')
FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`, schema, object)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Field
			var nullable string
			var defaultValue sql.NullString
			var comment sql.NullString
			if err := rows.Scan(&item.Name, &item.DataType, &nullable, &item.Ordinal, &item.Definition, &defaultValue, &comment); err != nil {
				return err
			}
			item.Nullable = strings.EqualFold(nullable, "yes") || strings.EqualFold(nullable, "y")
			item.Default = strings.TrimSpace(defaultValue.String)
			item.Comment = strings.TrimSpace(comment.String)
			out = append(out, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return m.markPrimaryKeys(ctx, db, source, schema, object, out)
	})
	if err != nil {
		return nil, err
	}
	metadataCacheSet(m, cacheKey, append([]Field(nil), out...))
	return out, nil
}
