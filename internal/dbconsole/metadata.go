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
	metadataCacheTTL        = 2 * time.Minute
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
}

func (m *Manager) Schemas(ctx context.Context, source Source) ([]Schema, error) {
	if source.Kind == KindRedis {
		return nil, errors.New("Redis 没有 schema")
	}
	cacheKey := source.ID + "\x00schemas"
	if cached, ok := metadataCacheGet[[]Schema](m, cacheKey); ok {
		return append([]Schema(nil), cached...), nil
	}
	ctx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(ctx); err != nil {
		return nil, err
	}
	defer m.release()
	db, err := m.sqlDB(source)
	if err != nil {
		return nil, err
	}
	query := "SELECT schema_name FROM information_schema.schemata ORDER BY schema_name"
	if source.Kind == KindOracle {
		query = "SELECT DISTINCT owner FROM all_objects WHERE object_type IN ('TABLE','VIEW','MATERIALIZED VIEW','FUNCTION','PROCEDURE','PACKAGE','SEQUENCE','SYNONYM','TRIGGER') ORDER BY owner"
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schema
	for rows.Next() && len(out) < 500 {
		var item Schema
		if err := rows.Scan(&item.Name); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	metadataCacheSet(m, cacheKey, append([]Schema(nil), out...))
	return out, nil
}

func (m *Manager) Objects(ctx context.Context, source Source, schema, search string) ([]Object, error) {
	if source.Kind == KindRedis {
		return nil, errors.New("Redis 没有表对象")
	}
	if schema == "" || len(schema) > 256 || len(search) > 256 {
		return nil, errors.New("schema 不能为空，且 schema/search 不能超过 256 字节")
	}
	cacheKey := fmt.Sprintf("%s\x00objects\x00%s\x00%s", source.ID, strings.ToUpper(schema), strings.ToUpper(search))
	if cached, ok := metadataCacheGet[[]Object](m, cacheKey); ok {
		return append([]Object(nil), cached...), nil
	}
	ctx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(ctx); err != nil {
		return nil, err
	}
	defer m.release()
	db, err := m.sqlDB(source)
	if err != nil {
		return nil, err
	}
	var rows *sql.Rows
	if source.Kind == KindOracle {
		rows, err = db.QueryContext(ctx, `SELECT owner, object_name, object_type FROM (
SELECT owner, object_name, object_type FROM all_objects
WHERE owner = :1 AND object_type IN ('TABLE','VIEW','MATERIALIZED VIEW','FUNCTION','PROCEDURE','PACKAGE','SEQUENCE','SYNONYM','TRIGGER') AND (:2 = '' OR UPPER(object_name) LIKE :3)
ORDER BY object_name) WHERE ROWNUM <= 500`, strings.ToUpper(schema), search, "%"+strings.ToUpper(search)+"%")
	} else {
		like := "%" + search + "%"
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
) objects ORDER BY object_type, object_name LIMIT 500`, schema, search, like, schema, search, like, schema, search, like)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Object
	for rows.Next() {
		var item Object
		if err := rows.Scan(&item.Schema, &item.Name, &item.Type); err != nil {
			return nil, err
		}
		item.Category = objectCategory(item.Type)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
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

func (m *Manager) Fields(ctx context.Context, source Source, schema, object string) ([]Field, error) {
	if source.Kind == KindRedis {
		return nil, errors.New("Redis 没有字段")
	}
	if schema == "" || object == "" || len(schema) > 256 || len(object) > 256 {
		return nil, errors.New("schema/object 不能为空且不能超过 256 字节")
	}
	cacheKey := fmt.Sprintf("%s\x00fields\x00%s\x00%s", source.ID, strings.ToUpper(schema), strings.ToUpper(object))
	if cached, ok := metadataCacheGet[[]Field](m, cacheKey); ok {
		return append([]Field(nil), cached...), nil
	}
	ctx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(ctx); err != nil {
		return nil, err
	}
	defer m.release()
	db, err := m.sqlDB(source)
	if err != nil {
		return nil, err
	}
	var rows *sql.Rows
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	metadataCacheSet(m, cacheKey, append([]Field(nil), out...))
	return out, nil
}
