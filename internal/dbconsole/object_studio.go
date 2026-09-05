package dbconsole

// Object studio contains the small, deliberately conservative API used by
// the database workbench's object designer.  The workbench never accepts raw
// DDL for these operations: callers send a typed change request, we validate
// every identifier/value and generate the final DDL here.  Keeping generation
// in this package also makes the preview and apply paths use exactly the same
// safety checks.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxObjectStudioColumns = 256
	maxObjectStudioSQL     = 1 << 20
	maxObjectStudioComment = 4000
)

var (
	objectIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$#]*$`)
	dataTypeRE         = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_ ]*(?:\s*\(\s*[0-9]+(?:\s*,\s*[0-9]+)?\s*\))?$`)
	numericRE          = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)$`)
	currentTimestampRE = regexp.MustCompile(`(?i)^CURRENT_TIMESTAMP(?:\([0-9]+\))?$`)
)

// ObjectStudioColumn describes one table column.  DataType intentionally
// accepts only a type name plus numeric precision/length; arbitrary SQL must
// never be put in this field.
type ObjectStudioColumn struct {
	Name          string `json:"name"`
	DataType      string `json:"data_type"`
	Nullable      *bool  `json:"nullable,omitempty"`
	Default       string `json:"default,omitempty"`
	Comment       string `json:"comment,omitempty"`
	PrimaryKey    bool   `json:"primary_key,omitempty"`
	AutoIncrement bool   `json:"auto_increment,omitempty"`
}

// ObjectStudioColumnChange is used by ALTER TABLE.  Action is one of
// add_column, alter_column, drop_column or rename_column.
type ObjectStudioColumnChange struct {
	Action  string             `json:"action"`
	OldName string             `json:"old_name,omitempty"`
	Column  ObjectStudioColumn `json:"column"`
}

type ObjectStudioIndex struct {
	Name    string   `json:"name"`
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique,omitempty"`
	Method  string   `json:"method,omitempty"`
}

type ObjectStudioConstraint struct {
	Name              string   `json:"name"`
	Table             string   `json:"table"`
	Type              string   `json:"type"` // primary, unique, foreign, check, not_null
	Columns           []string `json:"columns"`
	Expression        string   `json:"expression,omitempty"`
	ReferencedSchema  string   `json:"referenced_schema,omitempty"`
	ReferencedTable   string   `json:"referenced_table,omitempty"`
	ReferencedColumns []string `json:"referenced_columns,omitempty"`
	OnDelete          string   `json:"on_delete,omitempty"`
}

type ObjectStudioSequence struct {
	Name      string `json:"name"`
	StartWith int64  `json:"start_with,omitempty"`
	Increment int64  `json:"increment,omitempty"`
	MinValue  int64  `json:"min_value,omitempty"`
	MaxValue  int64  `json:"max_value,omitempty"`
	Cache     int64  `json:"cache,omitempty"`
	Cycle     bool   `json:"cycle,omitempty"`
	Order     bool   `json:"order,omitempty"`
}

type ObjectStudioSequenceInfo struct {
	Name      string `json:"name"`
	StartWith int64  `json:"start_with,omitempty"`
	Increment int64  `json:"increment"`
	MinValue  int64  `json:"min_value,omitempty"`
	MaxValue  int64  `json:"max_value,omitempty"`
	Cache     int64  `json:"cache,omitempty"`
	Cycle     bool   `json:"cycle"`
	Order     bool   `json:"order"`
	LastValue int64  `json:"last_value,omitempty"`
}

type ObjectStudioIndexDefinition struct {
	IndexInfo
	Schema string `json:"schema"`
	Table  string `json:"table"`
}

type ObjectStudioConstraintDefinition struct {
	ConstraintInfo
	Schema    string `json:"schema"`
	Table     string `json:"table"`
	Status    string `json:"status,omitempty"`
	Validated string `json:"validated,omitempty"`
}

// ObjectStudioChange is the common request for table/view/index/constraint/
// sequence creation and modification.  Action is create, alter or drop.
// ObjectType is table, view, index, constraint or sequence.
type ObjectStudioChange struct {
	Action     string                     `json:"action"`
	ObjectType string                     `json:"object_type"`
	Schema     string                     `json:"schema"`
	Name       string                     `json:"name"`
	Table      string                     `json:"table,omitempty"`
	Columns    []ObjectStudioColumn       `json:"columns,omitempty"`
	Changes    []ObjectStudioColumnChange `json:"changes,omitempty"`
	PrimaryKey []string                   `json:"primary_key,omitempty"`
	Definition string                     `json:"definition,omitempty"`
	Index      *ObjectStudioIndex         `json:"index,omitempty"`
	Constraint *ObjectStudioConstraint    `json:"constraint,omitempty"`
	Sequence   *ObjectStudioSequence      `json:"sequence,omitempty"`
	Confirm    bool                       `json:"confirm,omitempty"`
}

type ObjectStudioPlan struct {
	Kind         string   `json:"kind"`
	Action       string   `json:"action"`
	ObjectType   string   `json:"object_type"`
	Statements   []string `json:"statements"`
	Capabilities []string `json:"capabilities,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

type ObjectStudioApplyResult struct {
	Plan      ObjectStudioPlan `json:"plan"`
	Applied   int              `json:"applied"`
	Success   bool             `json:"success"`
	ElapsedMS int64            `json:"elapsed_ms"`
	Error     string           `json:"error,omitempty"`
}

// ObjectDependency is intentionally database-neutral.  Referenced fields
// may be empty when a database only exposes object-level dependencies.
type ObjectDependency struct {
	Schema           string `json:"schema"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	ReferencedSchema string `json:"referenced_schema"`
	ReferencedName   string `json:"referenced_name"`
	ReferencedType   string `json:"referenced_type"`
	DependencyType   string `json:"dependency_type,omitempty"`
}

type InvalidObject struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type ObjectStudioStructure struct {
	Schema       string                            `json:"schema"`
	Name         string                            `json:"name"`
	Type         string                            `json:"type"`
	Fields       []Field                           `json:"fields"`
	Indexes      []IndexInfo                       `json:"indexes"`
	Constraints  []ConstraintInfo                  `json:"constraints"`
	Dependencies []ObjectDependency                `json:"dependencies,omitempty"`
	DDL          string                            `json:"ddl,omitempty"`
	DDLSource    string                            `json:"ddl_source,omitempty"`
	DDLError     string                            `json:"ddl_error,omitempty"`
	SourceText   string                            `json:"source_text,omitempty"`
	Index        *ObjectStudioIndexDefinition      `json:"index,omitempty"`
	Constraint   *ObjectStudioConstraintDefinition `json:"constraint,omitempty"`
	Sequence     *ObjectStudioSequenceInfo         `json:"sequence,omitempty"`
	Status       string                            `json:"status,omitempty"`
	Valid        bool                              `json:"valid"`
	StatusKnown  bool                              `json:"status_known"`
}

type OracleCompileError struct {
	Schema      string `json:"schema,omitempty"`
	Name        string `json:"name,omitempty"`
	Type        string `json:"type,omitempty"`
	Sequence    int    `json:"sequence,omitempty"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	Position    int    `json:"position,omitempty"`
	ErrorNumber int    `json:"error_number,omitempty"`
	Attribute   string `json:"attribute,omitempty"`
	Text        string `json:"text"`
}

type OracleFunction struct {
	Schema   string               `json:"schema"`
	Name     string               `json:"name"`
	Type     string               `json:"type"`
	Status   string               `json:"status"`
	Valid    bool                 `json:"valid"`
	Source   string               `json:"source"`
	Errors   []OracleCompileError `json:"errors,omitempty"`
	LoadedAt string               `json:"loaded_at,omitempty"`
}

type OracleFunctionChange struct {
	Schema  string `json:"schema"`
	Name    string `json:"name"`
	Source  string `json:"source"`
	Replace bool   `json:"replace,omitempty"`
	Confirm bool   `json:"confirm,omitempty"`
}

type OracleFunctionCompileResult struct {
	Function       OracleFunction       `json:"function"`
	SQL            string               `json:"sql,omitempty"`
	Success        bool                 `json:"success"`
	Compiled       bool                 `json:"compiled"`
	ExecutionError string               `json:"execution_error,omitempty"`
	Errors         []OracleCompileError `json:"errors,omitempty"`
	ElapsedMS      int64                `json:"elapsed_ms"`
}

// Generic aliases make the API pleasant for non-UI callers while retaining
// the explicit ObjectStudio* names used by the HTTP contract.
type ObjectChange = ObjectStudioChange
type ObjectPlan = ObjectStudioPlan
type ObjectApplyResult = ObjectStudioApplyResult

// ObjectStudioCapabilities reports database differences without requiring a
// connection.  The UI can disable unsupported controls before opening a
// design form (notably sequences on MySQL).
func ObjectStudioCapabilities(kind string) map[string]bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == KindOracle {
		return map[string]bool{
			"table": true, "view": true, "index": true, "constraint": true,
			"sequence": true, "function": true, "function_compile": true,
		}
	}
	if kind == KindMySQL {
		return map[string]bool{
			"table": true, "view": true, "index": true, "constraint": true,
			"sequence": false, "function": false, "function_compile": false,
		}
	}
	return map[string]bool{}
}

func normalizeStudioType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "table", "tables", "表":
		return "table"
	case "view", "views", "视图":
		return "view"
	case "index", "indexes", "索引":
		return "index"
	case "constraint", "constraints", "约束":
		return "constraint"
	case "sequence", "sequences", "序列":
		return "sequence"
	default:
		return ""
	}
}

func normalizeStudioAction(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "create", "new", "add", "创建":
		return "create"
	case "alter", "update", "modify", "edit", "修改":
		return "alter"
	case "drop", "delete", "remove", "删除":
		return "drop"
	default:
		return ""
	}
}

func validateStudioIdentifier(label, name, kind string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%s 不能为空", label)
	}
	max := 128
	if kind == KindMySQL {
		max = 64
	}
	if len(name) > max {
		return fmt.Errorf("%s 不能超过 %d 字节", label, max)
	}
	if !objectIdentifierRE.MatchString(name) || kind == KindMySQL && strings.ContainsAny(name, "$#") {
		return fmt.Errorf("%s 只能包含字母、数字、下划线%s，且必须以字母或下划线开头", label, func() string {
			if kind == KindOracle {
				return "、$、#"
			}
			return ""
		}())
	}
	return nil
}

func validateStudioDataType(kind, value string) error {
	v := strings.TrimSpace(value)
	if v == "" || len(v) > 128 || strings.ContainsAny(v, "\x00;\r\n") || !dataTypeRE.MatchString(v) {
		return fmt.Errorf("数据类型 %q 非法：仅支持类型名及数字精度/长度", value)
	}
	upper := strings.ToUpper(strings.Join(strings.Fields(v), " "))
	// Restrict known vendor-specific types to their respective backend.  The
	// generic list covers common aliases without accepting an arbitrary SQL
	// fragment as a type declaration.
	known := map[string]bool{
		"CHAR": true, "CHARACTER": true, "VARCHAR": true, "VARCHAR2": true,
		"CHARACTER VARYING": true, "DOUBLE PRECISION": true,
		"NCHAR": true, "NVARCHAR": true, "NVARCHAR2": true, "TEXT": true,
		"TINYINT": true, "SMALLINT": true, "MEDIUMINT": true, "INT": true,
		"INTEGER": true, "BIGINT": true, "DECIMAL": true, "NUMERIC": true,
		"NUMBER": true, "FLOAT": true, "REAL": true, "DOUBLE": true,
		"DATE": true, "DATETIME": true, "TIMESTAMP": true, "TIME": true,
		"TIMESTAMP WITH TIME ZONE": true, "TIMESTAMP WITH LOCAL TIME ZONE": true,
		"BOOLEAN": true, "BOOL": true, "BINARY": true, "VARBINARY": true,
		"RAW": true, "BLOB": true, "CLOB": true, "NCLOB": true,
		"LONG": true, "JSON": true, "XMLTYPE": true,
	}
	base := strings.TrimSpace(strings.SplitN(upper, "(", 2)[0])
	if !known[base] {
		return fmt.Errorf("不支持的数据类型 %q", value)
	}
	if kind == KindOracle && (base == "TEXT" || base == "DATETIME" || base == "TINYINT" || base == "MEDIUMINT" || base == "INT" || base == "BIGINT" || base == "BOOLEAN" || base == "BOOL" || base == "JSON") {
		return fmt.Errorf("Oracle 不支持数据类型 %q", value)
	}
	if kind == KindMySQL && (base == "VARCHAR2" || base == "NVARCHAR2" || base == "NUMBER" || base == "RAW" || base == "NCLOB" || base == "XMLTYPE") {
		return fmt.Errorf("MySQL 不支持数据类型 %q", value)
	}
	return nil
}

func validateStudioDefault(value string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil
	}
	if len(v) > 512 || strings.ContainsAny(v, "\x00;\r\n") || strings.Contains(v, "--") || strings.Contains(v, "/*") || strings.Contains(v, "*/") {
		return errors.New("默认值包含非法字符")
	}
	if numericRE.MatchString(v) || strings.EqualFold(v, "NULL") || strings.EqualFold(v, "TRUE") || strings.EqualFold(v, "FALSE") || strings.EqualFold(v, "SYSDATE") || strings.EqualFold(v, "SYSTIMESTAMP") || strings.EqualFold(v, "CURRENT_DATE") || currentTimestampRE.MatchString(v) {
		return nil
	}
	if strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") {
		for i := 1; i < len(v)-1; i++ {
			if v[i] == '\'' {
				if i+1 < len(v)-1 && v[i+1] == '\'' {
					i++
					continue
				}
				return errors.New("默认字符串引号未正确转义")
			}
		}
		return nil
	}
	// Permit a small set of deterministic date conversion expressions used in
	// Oracle forms.  No user-supplied identifiers or operators are accepted.
	upper := strings.ToUpper(v)
	if strings.HasPrefix(upper, "TO_DATE('") || strings.HasPrefix(upper, "TO_TIMESTAMP('") || strings.EqualFold(v, "UUID()") {
		if !balancedParentheses(v) {
			return errors.New("默认值括号不匹配")
		}
		for _, r := range v {
			if !(r == '\'' || r == '(' || r == ')' || r == ',' || r == ' ' || r == '_' || r == '-' || r == ':' || r == '.' || r == '/' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
				return errors.New("默认值表达式包含非法字符")
			}
		}
		return nil
	}
	return errors.New("默认值只支持数字、字符串、NULL、当前时间或受控日期函数")
}

func balancedParentheses(value string) bool {
	depth := 0
	inString := false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\'':
			if inString && i+1 < len(value) && value[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
		case '(':
			if !inString {
				depth++
			}
		case ')':
			if !inString {
				depth--
				if depth < 0 {
					return false
				}
			}
		}
	}
	return !inString && depth == 0
}

func validateStudioComment(value string) error {
	if len(value) > maxObjectStudioComment || strings.ContainsRune(value, '\x00') {
		return errors.New("注释过长或包含非法字符")
	}
	return nil
}

func sqlStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func normalizeColumnNullable(v *bool) bool {
	return v == nil || *v
}

func validateStudioColumns(kind string, columns []ObjectStudioColumn) error {
	if len(columns) == 0 {
		return errors.New("表至少需要一列")
	}
	if len(columns) > maxObjectStudioColumns {
		return fmt.Errorf("列数量不能超过 %d", maxObjectStudioColumns)
	}
	seen := map[string]bool{}
	for _, col := range columns {
		if err := validateStudioIdentifier("列名", col.Name, kind); err != nil {
			return err
		}
		key := strings.ToUpper(col.Name)
		if seen[key] {
			return fmt.Errorf("列名重复: %s", col.Name)
		}
		seen[key] = true
		if err := validateStudioDataType(kind, col.DataType); err != nil {
			return err
		}
		if err := validateStudioDefault(col.Default); err != nil {
			return fmt.Errorf("列 %s: %w", col.Name, err)
		}
		if err := validateStudioComment(col.Comment); err != nil {
			return fmt.Errorf("列 %s: %w", col.Name, err)
		}
	}
	return nil
}

func validateStudioColumnNames(kind string, names []string, allowEmpty bool) error {
	if len(names) == 0 && !allowEmpty {
		return errors.New("至少需要一个列")
	}
	if len(names) > maxObjectStudioColumns {
		return fmt.Errorf("列数量不能超过 %d", maxObjectStudioColumns)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if err := validateStudioIdentifier("列名", name, kind); err != nil {
			return err
		}
		key := strings.ToUpper(name)
		if seen[key] {
			return fmt.Errorf("列名重复: %s", name)
		}
		seen[key] = true
	}
	return nil
}

func validateStudioStringList(names []string, label, kind string) error {
	seen := map[string]bool{}
	for _, name := range names {
		if err := validateStudioIdentifier(label, name, kind); err != nil {
			return err
		}
		key := strings.ToUpper(name)
		if seen[key] {
			return fmt.Errorf("%s 重复: %s", label, name)
		}
		seen[key] = true
	}
	return nil
}

// ValidateObjectStudioChange validates a change without connecting to a
// database.  It is exported for handler tests and for other local clients.
func ValidateObjectStudioChange(source Source, change ObjectStudioChange) error {
	kind := normalizeStudioType(change.ObjectType)
	if kind == "" {
		return errors.New("object_type 仅支持 table/view/index/constraint/sequence")
	}
	action := normalizeStudioAction(change.Action)
	if action == "" {
		return errors.New("action 仅支持 create/alter/drop")
	}
	if source.Kind != KindOracle && source.Kind != KindMySQL {
		return errors.New("Redis 不支持对象设计器")
	}
	if err := validateStudioIdentifier("schema", change.Schema, source.Kind); err != nil {
		return err
	}
	if err := validateStudioIdentifier("对象名", change.Name, source.Kind); err != nil {
		return err
	}
	if action == "drop" && !change.Confirm {
		return errors.New("删除对象需要 confirm=true")
	}
	if action == "create" && kind == "table" {
		if err := validateStudioColumns(source.Kind, change.Columns); err != nil {
			return err
		}
	}
	if action == "alter" && kind == "table" {
		if len(change.Changes) == 0 {
			return errors.New("ALTER TABLE 至少需要一个 changes 项")
		}
		if len(change.Changes) > maxObjectStudioColumns {
			return fmt.Errorf("changes 不能超过 %d 项", maxObjectStudioColumns)
		}
		for _, item := range change.Changes {
			op := strings.ToLower(strings.TrimSpace(item.Action))
			switch op {
			case "add_column", "alter_column":
				if err := validateStudioColumns(source.Kind, []ObjectStudioColumn{item.Column}); err != nil {
					return err
				}
			case "drop_column":
				if !change.Confirm {
					return errors.New("删除列需要 confirm=true")
				}
				if err := validateStudioIdentifier("列名", item.OldName, source.Kind); err != nil {
					return err
				}
			case "rename_column":
				if err := validateStudioIdentifier("旧列名", item.OldName, source.Kind); err != nil {
					return err
				}
				if err := validateStudioIdentifier("新列名", item.Column.Name, source.Kind); err != nil {
					return err
				}
			default:
				return fmt.Errorf("不支持的列操作 %q", item.Action)
			}
		}
	}
	if kind == "table" && action == "create" {
		if source.Kind == KindOracle {
			for _, col := range change.Columns {
				if col.AutoIncrement {
					return errors.New("Oracle 11g 不支持通过对象设计器创建 identity/auto_increment 列，请使用序列或触发器")
				}
			}
		}
		if err := validateStudioStringList(change.PrimaryKey, "主键列", source.Kind); err != nil {
			return err
		}
		if len(change.PrimaryKey) > 0 {
			columns := make(map[string]bool, len(change.Columns))
			for _, col := range change.Columns {
				columns[strings.ToUpper(col.Name)] = true
			}
			for _, name := range change.PrimaryKey {
				if !columns[strings.ToUpper(name)] {
					return fmt.Errorf("主键列 %s 不在表列定义中", name)
				}
			}
		}
	}
	if kind == "view" {
		if action != "drop" {
			if strings.TrimSpace(change.Definition) == "" || len(change.Definition) > maxObjectStudioSQL {
				return errors.New("视图定义不能为空且不能超过 1MB")
			}
			if err := ValidateReadOnlySQL(source.Kind, strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(change.Definition), ";"))); err != nil {
				return fmt.Errorf("视图定义必须是只读 SELECT/WITH: %w", err)
			}
		}
	}
	if kind == "index" {
		idx := change.Index
		if idx == nil {
			idx = &ObjectStudioIndex{Name: change.Name, Table: change.Table}
		}
		if err := validateStudioIdentifier("索引名", idx.Name, source.Kind); err != nil {
			return err
		}
		if err := validateStudioIdentifier("表名", idx.Table, source.Kind); err != nil {
			return err
		}
		if action != "drop" {
			if err := validateStudioColumnNames(source.Kind, idx.Columns, false); err != nil {
				return err
			}
			if method := strings.TrimSpace(idx.Method); method != "" && !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_ ]{0,30}$`).MatchString(method) {
				return errors.New("索引方法名非法")
			}
		}
	}
	if kind == "constraint" {
		c := change.Constraint
		if c == nil {
			c = &ObjectStudioConstraint{Name: change.Name, Table: change.Table}
		}
		if err := validateStudioIdentifier("约束名", c.Name, source.Kind); err != nil {
			return err
		}
		if err := validateStudioIdentifier("表名", c.Table, source.Kind); err != nil {
			return err
		}
		if action != "drop" {
			ctype := strings.ToLower(strings.TrimSpace(c.Type))
			switch ctype {
			case "primary", "primary key", "unique", "foreign", "foreign key", "check", "not_null", "not null":
			default:
				return errors.New("约束类型仅支持 primary/unique/foreign/check/not_null")
			}
			// CHECK constraints are expression-based and do not require an
			// explicit column list. All other supported constraint types do.
			if err := validateStudioColumnNames(source.Kind, c.Columns, ctype == "check"); err != nil {
				return err
			}
			if ctype == "foreign" || ctype == "foreign key" {
				if err := validateStudioIdentifier("引用 schema", c.ReferencedSchema, source.Kind); err != nil {
					return err
				}
				if err := validateStudioIdentifier("引用表名", c.ReferencedTable, source.Kind); err != nil {
					return err
				}
				if len(c.ReferencedColumns) != len(c.Columns) {
					return errors.New("外键引用列数量必须与本地列一致")
				}
				if err := validateStudioColumnNames(source.Kind, c.ReferencedColumns, false); err != nil {
					return err
				}
			}
			if ctype == "check" {
				if strings.TrimSpace(c.Expression) == "" || len(c.Expression) > 2000 || !safeConstraintExpression(c.Expression) {
					return errors.New("CHECK 表达式非法")
				}
			}
			if c.OnDelete != "" && !isAllowedOnDelete(c.OnDelete) {
				return errors.New("ON DELETE 仅支持 CASCADE/SET NULL/RESTRICT/NO ACTION")
			}
		}
	}
	if kind == "sequence" {
		if source.Kind == KindMySQL {
			return errors.New("MySQL 不支持原生序列设计")
		}
		seq := change.Sequence
		if seq == nil {
			seq = &ObjectStudioSequence{Name: change.Name}
		}
		if err := validateStudioIdentifier("序列名", seq.Name, source.Kind); err != nil {
			return err
		}
		if action != "drop" {
			if seq.StartWith < 0 || seq.Increment == 0 || seq.Cache < 0 {
				return errors.New("序列 start_with/increment/cache 参数非法")
			}
			if seq.MaxValue != 0 && seq.MinValue != 0 && seq.MinValue >= seq.MaxValue {
				return errors.New("序列 min_value 必须小于 max_value")
			}
		}
	}
	return nil
}

func safeConstraintExpression(value string) bool {
	if strings.ContainsAny(value, "\x00;\r\n") || strings.Contains(value, "--") || strings.Contains(value, "/*") || strings.Contains(value, "*/") {
		return false
	}
	return balancedParentheses(value)
}

func isAllowedOnDelete(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "CASCADE", "SET NULL", "RESTRICT", "NO ACTION":
		return true
	default:
		return false
	}
}

func studioColumnSQL(kind string, col ObjectStudioColumn) string {
	var b strings.Builder
	b.WriteString(quoteIdent(kind, col.Name))
	b.WriteByte(' ')
	b.WriteString(strings.Join(strings.Fields(strings.TrimSpace(col.DataType)), " "))
	if col.AutoIncrement {
		if kind == KindMySQL {
			b.WriteString(" AUTO_INCREMENT")
		}
	}
	if strings.TrimSpace(col.Default) != "" {
		b.WriteString(" DEFAULT ")
		b.WriteString(strings.TrimSpace(col.Default))
	}
	if col.Nullable != nil && !normalizeColumnNullable(col.Nullable) {
		b.WriteString(" NOT NULL")
	}
	if col.Comment != "" && kind == KindMySQL {
		b.WriteString(" COMMENT ")
		b.WriteString(sqlStringLiteral(col.Comment))
	}
	return b.String()
}

func qualifiedStudioName(kind, schema, object string) string {
	return quoteIdent(kind, schema) + "." + quoteIdent(kind, object)
}

func indexForChange(change ObjectStudioChange) ObjectStudioIndex {
	if change.Index != nil {
		idx := *change.Index
		if idx.Name == "" {
			idx.Name = change.Name
		}
		if idx.Table == "" {
			idx.Table = change.Table
		}
		return idx
	}
	return ObjectStudioIndex{Name: change.Name, Table: change.Table}
}

func constraintForChange(change ObjectStudioChange) ObjectStudioConstraint {
	if change.Constraint != nil {
		c := *change.Constraint
		if c.Name == "" {
			c.Name = change.Name
		}
		if c.Table == "" {
			c.Table = change.Table
		}
		return c
	}
	return ObjectStudioConstraint{Name: change.Name, Table: change.Table}
}

func sequenceForChange(change ObjectStudioChange) ObjectStudioSequence {
	if change.Sequence != nil {
		seq := *change.Sequence
		if seq.Name == "" {
			seq.Name = change.Name
		}
		return seq
	}
	return ObjectStudioSequence{Name: change.Name}
}

// BuildObjectStudioPlan returns the exact statements used by ApplyObjectStudio.
func BuildObjectStudioPlan(source Source, change ObjectStudioChange) (ObjectStudioPlan, error) {
	if err := ValidateObjectStudioChange(source, change); err != nil {
		return ObjectStudioPlan{}, err
	}
	kind := normalizeStudioType(change.ObjectType)
	action := normalizeStudioAction(change.Action)
	plan := ObjectStudioPlan{Kind: source.Kind, Action: action, ObjectType: kind, Capabilities: []string{kind}}
	qualified := qualifiedStudioName(source.Kind, change.Schema, change.Name)
	switch kind {
	case "table":
		switch action {
		case "create":
			parts := make([]string, 0, len(change.Columns)+1)
			for _, col := range change.Columns {
				parts = append(parts, "  "+studioColumnSQL(source.Kind, col))
			}
			primaryKey := append([]string(nil), change.PrimaryKey...)
			if len(primaryKey) == 0 {
				for _, col := range change.Columns {
					if col.PrimaryKey {
						primaryKey = append(primaryKey, col.Name)
					}
				}
			}
			if len(primaryKey) > 0 {
				cols := make([]string, len(primaryKey))
				for i, col := range primaryKey {
					cols[i] = quoteIdent(source.Kind, col)
				}
				parts = append(parts, "  PRIMARY KEY ("+strings.Join(cols, ", ")+")")
			}
			stmt := "CREATE TABLE " + qualified + " (\n" + strings.Join(parts, ",\n") + "\n)"
			plan.Statements = []string{stmt}
			for _, col := range change.Columns {
				if source.Kind == KindOracle && col.Comment != "" {
					plan.Statements = append(plan.Statements, "COMMENT ON COLUMN "+qualified+"."+quoteIdent(source.Kind, col.Name)+" IS "+sqlStringLiteral(col.Comment))
				}
			}
		case "alter":
			for _, item := range change.Changes {
				op := strings.ToLower(strings.TrimSpace(item.Action))
				switch op {
				case "add_column":
					plan.Statements = append(plan.Statements, "ALTER TABLE "+qualified+" ADD "+studioColumnSQL(source.Kind, item.Column))
				case "alter_column":
					if source.Kind == KindOracle {
						plan.Statements = append(plan.Statements, "ALTER TABLE "+qualified+" MODIFY "+studioColumnSQL(source.Kind, item.Column))
					} else {
						plan.Statements = append(plan.Statements, "ALTER TABLE "+qualified+" MODIFY COLUMN "+studioColumnSQL(source.Kind, item.Column))
					}
				case "drop_column":
					plan.Statements = append(plan.Statements, "ALTER TABLE "+qualified+" DROP COLUMN "+quoteIdent(source.Kind, item.OldName))
				case "rename_column":
					if source.Kind == KindOracle {
						plan.Statements = append(plan.Statements, "ALTER TABLE "+qualified+" RENAME COLUMN "+quoteIdent(source.Kind, item.OldName)+" TO "+quoteIdent(source.Kind, item.Column.Name))
					} else {
						plan.Statements = append(plan.Statements, "ALTER TABLE "+qualified+" RENAME COLUMN "+quoteIdent(source.Kind, item.OldName)+" TO "+quoteIdent(source.Kind, item.Column.Name))
					}
				}
			}
		case "drop":
			stmt := "DROP TABLE " + qualified
			if source.Kind == KindOracle {
				stmt += " CASCADE CONSTRAINTS"
			}
			plan.Statements = []string{stmt}
		}
	case "view":
		switch action {
		case "create":
			if source.Kind == KindOracle {
				plan.Statements = []string{"CREATE OR REPLACE VIEW " + qualified + " AS " + strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(change.Definition), ";"))}
			} else {
				plan.Statements = []string{"CREATE OR REPLACE VIEW " + qualified + " AS " + strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(change.Definition), ";"))}
			}
		case "alter":
			plan.Statements = []string{"CREATE OR REPLACE VIEW " + qualified + " AS " + strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(change.Definition), ";"))}
		case "drop":
			plan.Statements = []string{"DROP VIEW " + qualified}
		}
	case "index":
		idx := indexForChange(change)
		idxCols := make([]string, len(idx.Columns))
		for i, col := range idx.Columns {
			idxCols[i] = quoteIdent(source.Kind, col)
		}
		create := "CREATE "
		if idx.Unique {
			create += "UNIQUE "
		}
		create += "INDEX " + qualifiedStudioName(source.Kind, change.Schema, idx.Name) + " ON " + qualifiedStudioName(source.Kind, change.Schema, idx.Table) + " (" + strings.Join(idxCols, ", ") + ")"
		if strings.TrimSpace(idx.Method) != "" && source.Kind == KindMySQL {
			create += " USING " + strings.ToUpper(strings.TrimSpace(idx.Method))
		}
		drop := "DROP INDEX " + qualifiedStudioName(source.Kind, change.Schema, idx.Name)
		if source.Kind == KindMySQL {
			drop = "ALTER TABLE " + qualifiedStudioName(source.Kind, change.Schema, idx.Table) + " DROP INDEX " + quoteIdent(source.Kind, idx.Name)
		}
		switch action {
		case "create":
			plan.Statements = []string{create}
		case "alter":
			plan.Statements = []string{drop, create}
			plan.Warnings = append(plan.Warnings, "数据库没有通用 ALTER INDEX；修改索引将先删除再重建")
		case "drop":
			plan.Statements = []string{drop}
		}
	case "constraint":
		c := constraintForChange(change)
		local := make([]string, len(c.Columns))
		for i, col := range c.Columns {
			local[i] = quoteIdent(source.Kind, col)
		}
		qualifiedTable := qualifiedStudioName(source.Kind, change.Schema, c.Table)
		ctype := strings.ToLower(strings.TrimSpace(c.Type))
		constraintClause := ""
		switch ctype {
		case "primary", "primary key":
			constraintClause = "PRIMARY KEY (" + strings.Join(local, ", ") + ")"
		case "unique":
			constraintClause = "UNIQUE (" + strings.Join(local, ", ") + ")"
		case "foreign", "foreign key":
			refs := make([]string, len(c.ReferencedColumns))
			for i, col := range c.ReferencedColumns {
				refs[i] = quoteIdent(source.Kind, col)
			}
			constraintClause = "FOREIGN KEY (" + strings.Join(local, ", ") + ") REFERENCES " + qualifiedStudioName(source.Kind, c.ReferencedSchema, c.ReferencedTable) + " (" + strings.Join(refs, ", ") + ")"
			if c.OnDelete != "" {
				constraintClause += " ON DELETE " + strings.ToUpper(strings.TrimSpace(c.OnDelete))
			}
		case "check":
			constraintClause = "CHECK (" + strings.TrimSpace(c.Expression) + ")"
		case "not_null", "not null":
			// A named NOT NULL constraint has no portable standalone syntax;
			// represent it as a column modification instead and require one column.
			if len(c.Columns) != 1 {
				return ObjectStudioPlan{}, errors.New("NOT NULL 约束必须恰好包含一列")
			}
			if source.Kind != KindOracle {
				return ObjectStudioPlan{}, errors.New("MySQL 修改 NOT NULL 必须同时提供完整列类型；请在表字段设计器中修改该列")
			}
			nullability := " NOT NULL"
			if action == "drop" {
				nullability = " NULL"
			}
			plan.Statements = []string{"ALTER TABLE " + qualifiedTable + " MODIFY " + quoteIdent(source.Kind, c.Columns[0]) + nullability}
		}
		if ctype != "not_null" && ctype != "not null" {
			add := "ALTER TABLE " + qualifiedTable + " ADD CONSTRAINT " + quoteIdent(source.Kind, c.Name) + " " + constraintClause
			drop := "ALTER TABLE " + qualifiedTable + " DROP CONSTRAINT " + quoteIdent(source.Kind, c.Name)
			if source.Kind == KindMySQL {
				switch ctype {
				case "primary", "primary key":
					drop = "ALTER TABLE " + qualifiedTable + " DROP PRIMARY KEY"
				case "unique":
					drop = "ALTER TABLE " + qualifiedTable + " DROP INDEX " + quoteIdent(source.Kind, c.Name)
				case "foreign", "foreign key":
					drop = "ALTER TABLE " + qualifiedTable + " DROP FOREIGN KEY " + quoteIdent(source.Kind, c.Name)
				case "check":
					drop = "ALTER TABLE " + qualifiedTable + " DROP CHECK " + quoteIdent(source.Kind, c.Name)
				}
			}
			switch action {
			case "create":
				plan.Statements = []string{add}
			case "alter":
				plan.Statements = []string{drop, add}
			case "drop":
				plan.Statements = []string{drop}
			}
		}
	case "sequence":
		seq := sequenceForChange(change)
		name := qualifiedStudioName(source.Kind, change.Schema, seq.Name)
		switch action {
		case "create":
			parts := []string{"CREATE SEQUENCE " + name}
			if seq.StartWith != 0 {
				parts = append(parts, "START WITH "+strconv.FormatInt(seq.StartWith, 10))
			}
			if seq.Increment != 0 {
				parts = append(parts, "INCREMENT BY "+strconv.FormatInt(seq.Increment, 10))
			}
			if seq.MinValue != 0 {
				parts = append(parts, "MINVALUE "+strconv.FormatInt(seq.MinValue, 10))
			}
			if seq.MaxValue != 0 {
				parts = append(parts, "MAXVALUE "+strconv.FormatInt(seq.MaxValue, 10))
			}
			if seq.Cache != 0 {
				parts = append(parts, "CACHE "+strconv.FormatInt(seq.Cache, 10))
			}
			if seq.Cycle {
				parts = append(parts, "CYCLE")
			}
			if seq.Order {
				parts = append(parts, "ORDER")
			}
			plan.Statements = []string{strings.Join(parts, " ")}
		case "alter":
			parts := []string{"ALTER SEQUENCE " + name}
			if seq.Increment != 0 {
				parts = append(parts, "INCREMENT BY "+strconv.FormatInt(seq.Increment, 10))
			}
			if seq.MinValue != 0 {
				parts = append(parts, "MINVALUE "+strconv.FormatInt(seq.MinValue, 10))
			}
			if seq.MaxValue != 0 {
				parts = append(parts, "MAXVALUE "+strconv.FormatInt(seq.MaxValue, 10))
			}
			if seq.Cache != 0 {
				parts = append(parts, "CACHE "+strconv.FormatInt(seq.Cache, 10))
			}
			if seq.Cycle {
				parts = append(parts, "CYCLE")
			} else {
				parts = append(parts, "NOCYCLE")
			}
			if seq.Order {
				parts = append(parts, "ORDER")
			} else {
				parts = append(parts, "NOORDER")
			}
			plan.Statements = []string{strings.Join(parts, " ")}
		case "drop":
			plan.Statements = []string{"DROP SEQUENCE " + name}
		}
	}
	if len(plan.Statements) == 0 {
		return ObjectStudioPlan{}, errors.New("未生成任何 DDL")
	}
	for _, stmt := range plan.Statements {
		if len(stmt) > maxObjectStudioSQL {
			return ObjectStudioPlan{}, errors.New("生成的 DDL 超过 1MB")
		}
	}
	return plan, nil
}

func (m *Manager) PreviewObjectStudio(source Source, change ObjectStudioChange) (ObjectStudioPlan, error) {
	return BuildObjectStudioPlan(source, change)
}

// PreviewObjectChange/ApplyObjectChange are short aliases kept for callers
// that do not need to know the UI feature name.  They intentionally delegate
// to the same implementation and do not expose a raw-SQL escape hatch.
func (m *Manager) PreviewObjectChange(source Source, change ObjectStudioChange) (ObjectStudioPlan, error) {
	return m.PreviewObjectStudio(source, change)
}

func (m *Manager) ApplyObjectChange(ctx context.Context, source Source, change ObjectStudioChange) (ObjectStudioApplyResult, error) {
	return m.ApplyObjectStudio(ctx, source, change)
}

// IndexDefinition reads one index by its own name.  The older Indexes method
// intentionally takes a table name (it is used by the result viewer), while
// object design forms need the inverse lookup.
func (m *Manager) IndexDefinition(ctx context.Context, source Source, schema, index string) (ObjectStudioIndexDefinition, error) {
	var out ObjectStudioIndexDefinition
	if source.Kind != KindOracle && source.Kind != KindMySQL {
		return out, errors.New("Redis 不支持索引")
	}
	if err := validateStudioIdentifier("schema", schema, source.Kind); err != nil {
		return out, err
	}
	if err := validateStudioIdentifier("索引名", index, source.Kind); err != nil {
		return out, err
	}
	out.Schema = schema
	out.Name = index
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var rows *sql.Rows
		var err error
		if source.Kind == KindOracle {
			rows, err = db.QueryContext(ctx, `SELECT index_name, index_type, uniqueness, table_name FROM all_indexes WHERE owner = :1 AND index_name = :2`, strings.ToUpper(schema), strings.ToUpper(index))
		} else {
			rows, err = db.QueryContext(ctx, `SELECT index_name, index_type, non_unique, table_name FROM information_schema.statistics WHERE table_schema = ? AND index_name = ? GROUP BY index_name, index_type, non_unique, table_name`, schema, index)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return err
			}
			return fmt.Errorf("索引不存在: %s.%s", schema, index)
		}
		if source.Kind == KindOracle {
			if err := rows.Scan(&out.Name, &out.Type, &out.Uniqueness, &out.Table); err != nil {
				return err
			}
		} else {
			var nonUnique int
			if err := rows.Scan(&out.Name, &out.Type, &nonUnique, &out.Table); err != nil {
				return err
			}
			if nonUnique == 0 {
				out.Uniqueness = "UNIQUE"
			} else {
				out.Uniqueness = "NONUNIQUE"
			}
		}
		return rows.Err()
	})
	if err != nil {
		return out, err
	}
	// The two metadata queries are deliberately separate: this works on
	// Oracle 11g and avoids relying on LISTAGG's length/error behaviour.
	err = m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var rows *sql.Rows
		var err error
		if source.Kind == KindOracle {
			rows, err = db.QueryContext(ctx, `SELECT column_name FROM all_ind_columns WHERE index_owner = :1 AND index_name = :2 ORDER BY column_position`, strings.ToUpper(schema), strings.ToUpper(index))
		} else {
			rows, err = db.QueryContext(ctx, `SELECT column_name FROM information_schema.statistics WHERE table_schema = ? AND index_name = ? ORDER BY seq_in_index`, schema, index)
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
			out.Columns = append(out.Columns, name)
		}
		return rows.Err()
	})
	return out, err
}

func (m *Manager) ConstraintDefinition(ctx context.Context, source Source, schema, name string) (ObjectStudioConstraintDefinition, error) {
	var out ObjectStudioConstraintDefinition
	if source.Kind != KindOracle && source.Kind != KindMySQL {
		return out, errors.New("Redis 不支持约束")
	}
	if err := validateStudioIdentifier("schema", schema, source.Kind); err != nil {
		return out, err
	}
	if err := validateStudioIdentifier("约束名", name, source.Kind); err != nil {
		return out, err
	}
	out.Schema, out.Name = schema, name
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var table, typ string
		if source.Kind == KindOracle {
			var status, validated sql.NullString
			err := db.QueryRowContext(ctx, `SELECT table_name, constraint_type, status, validated FROM all_constraints WHERE owner = :1 AND constraint_name = :2`, strings.ToUpper(schema), strings.ToUpper(name)).Scan(&table, &typ, &status, &validated)
			if err == sql.ErrNoRows {
				return fmt.Errorf("约束不存在: %s.%s", schema, name)
			}
			if err != nil {
				return err
			}
			out.Status, out.Validated = status.String, validated.String
		} else {
			err := db.QueryRowContext(ctx, `SELECT table_name, constraint_type FROM information_schema.table_constraints WHERE constraint_schema = ? AND constraint_name = ?`, schema, name).Scan(&table, &typ)
			if err == sql.ErrNoRows {
				return fmt.Errorf("约束不存在: %s.%s", schema, name)
			}
			if err != nil {
				return err
			}
		}
		out.Table = table
		out.Type = constraintLabel(typ)
		if source.Kind == KindOracle {
			rows, err := db.QueryContext(ctx, `SELECT column_name FROM all_cons_columns WHERE owner = :1 AND constraint_name = :2 ORDER BY position`, strings.ToUpper(schema), strings.ToUpper(name))
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var column string
				if err := rows.Scan(&column); err != nil {
					return err
				}
				if out.Columns != "" {
					out.Columns += ", "
				}
				out.Columns += column
			}
			return rows.Err()
		}
		rows, err := db.QueryContext(ctx, `SELECT column_name FROM information_schema.key_column_usage WHERE constraint_schema = ? AND constraint_name = ? ORDER BY ordinal_position`, schema, name)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				return err
			}
			if out.Columns != "" {
				out.Columns += ", "
			}
			out.Columns += column
		}
		return rows.Err()
	})
	return out, err
}

func (m *Manager) SequenceDefinition(ctx context.Context, source Source, schema, name string) (ObjectStudioSequenceInfo, error) {
	var out ObjectStudioSequenceInfo
	if source.Kind != KindOracle {
		return out, errors.New("只有 Oracle 支持序列元数据")
	}
	if err := validateStudioIdentifier("schema", schema, source.Kind); err != nil {
		return out, err
	}
	if err := validateStudioIdentifier("序列名", name, source.Kind); err != nil {
		return out, err
	}
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var minValue, maxValue, increment, cache, last sql.NullInt64
		var cycle, order sql.NullString
		err := db.QueryRowContext(ctx, `SELECT sequence_name, min_value, max_value, increment_by, cycle_flag, order_flag, cache_size, last_number FROM all_sequences WHERE sequence_owner = :1 AND sequence_name = :2`, strings.ToUpper(schema), strings.ToUpper(name)).Scan(&out.Name, &minValue, &maxValue, &increment, &cycle, &order, &cache, &last)
		if err == sql.ErrNoRows {
			return fmt.Errorf("序列不存在: %s.%s", schema, name)
		}
		if err != nil {
			return err
		}
		out.MinValue, out.MaxValue, out.Increment, out.Cache, out.LastValue = minValue.Int64, maxValue.Int64, increment.Int64, cache.Int64, last.Int64
		out.Cycle, out.Order = strings.EqualFold(cycle.String, "Y"), strings.EqualFold(order.String, "Y")
		return nil
	})
	return out, err
}

// ApplyObjectStudio executes only the statements generated from the validated
// typed request.  Each statement is executed through the existing bounded SQL
// pool.  DDL autocommits on both supported databases; a partial result is
// returned if an index/constraint rebuild fails after its drop statement.
func (m *Manager) ApplyObjectStudio(ctx context.Context, source Source, change ObjectStudioChange) (ObjectStudioApplyResult, error) {
	if !source.DDLAllowed() {
		return ObjectStudioApplyResult{}, errors.New("当前数据源未开启 DDL 能力（allow_ddl=true），或处于只读模式")
	}
	if source.IsProduction() && !change.Confirm {
		return ObjectStudioApplyResult{}, errors.New("生产数据源执行对象变更需要 confirm=true")
	}
	plan, err := BuildObjectStudioPlan(source, change)
	if err != nil {
		return ObjectStudioApplyResult{}, err
	}
	started := time.Now()
	result := ObjectStudioApplyResult{Plan: plan}
	var execErr error
	err = m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		for i, stmt := range plan.Statements {
			// Revalidate generated DDL immediately before execution.  This guards
			// against future code accidentally adding a raw-SQL path to the plan.
			info, validateErr := ValidateSQL(source.Kind, stmt)
			isComment := strings.HasPrefix(strings.ToUpper(strings.TrimSpace(stmt)), "COMMENT ON ")
			if validateErr != nil && !isComment || !isComment && info.Type != "DDL" {
				if validateErr != nil {
					return validateErr
				}
				return errors.New("对象设计器只允许执行 DDL")
			}
			if _, execErr = db.ExecContext(ctx, stmt); execErr != nil {
				result.Error = execErr.Error()
				result.Applied = i
				if i > 0 && isConnectionFailure(execErr) {
					// withSQL retries connection failures.  A retry after one or
					// more DDL statements would be unsafe because DDL autocommits;
					// stop here and report the partial result instead.
					return errors.New("对象变更已部分执行，连接中断后已停止自动重试")
				}
				return execErr
			}
			result.Applied = i + 1
		}
		return nil
	})
	result.ElapsedMS = time.Since(started).Milliseconds()
	result.Success = err == nil
	if err != nil {
		if result.Error == "" {
			result.Error = err.Error()
		}
		return result, err
	}
	m.InvalidateMetadata(source.ID)
	return result, nil
}

// ObjectStudioStructure loads the existing structure and enriches the legacy
// InspectObject payload with dependencies and Oracle validity state.
func (m *Manager) ObjectStudioStructure(ctx context.Context, source Source, schema, object, objectType string) (ObjectStudioStructure, error) {
	if source.Kind == KindRedis {
		return ObjectStudioStructure{}, errors.New("Redis 不支持对象结构")
	}
	if err := validateStudioIdentifier("schema", schema, source.Kind); err != nil {
		return ObjectStudioStructure{}, err
	}
	if err := validateStudioIdentifier("对象名", object, source.Kind); err != nil {
		return ObjectStudioStructure{}, err
	}
	typ := strings.ToLower(strings.TrimSpace(objectType))
	if typ == "" {
		typ = "table"
	}
	out := ObjectStudioStructure{Schema: schema, Name: object, Type: strings.ToUpper(strings.TrimSpace(objectType)), Fields: []Field{}, Indexes: []IndexInfo{}, Constraints: []ConstraintInfo{}, Dependencies: []ObjectDependency{}, Valid: true}
	if out.Type == "" {
		out.Type = "TABLE"
	}
	var err error
	switch normalizeStudioType(typ) {
	case "index":
		var item ObjectStudioIndexDefinition
		item, err = m.IndexDefinition(ctx, source, schema, object)
		if err == nil {
			out.Index = &item
			out.Indexes = []IndexInfo{item.IndexInfo}
		}
	case "constraint":
		var item ObjectStudioConstraintDefinition
		item, err = m.ConstraintDefinition(ctx, source, schema, object)
		if err == nil {
			out.Constraint = &item
			out.Constraints = []ConstraintInfo{item.ConstraintInfo}
		}
	case "sequence":
		var item ObjectStudioSequenceInfo
		item, err = m.SequenceDefinition(ctx, source, schema, object)
		if err == nil {
			out.Sequence = &item
		}
	default:
		var inspect ObjectInspect
		inspect, err = m.InspectObject(ctx, source, schema, object, objectType)
		if err == nil {
			out.Fields, out.Indexes, out.Constraints = inspect.Fields, inspect.Indexes, inspect.Constraints
			out.DDL, out.DDLSource, out.DDLError, out.SourceText = inspect.DDL, inspect.DDLSource, inspect.DDLError, inspect.SourceText
		}
	}
	if err != nil {
		return ObjectStudioStructure{}, err
	}
	if out.DDL == "" && source.Kind == KindOracle {
		// DBMS_METADATA is preferable but may be unavailable to a restricted
		// account.  Keep object-specific structure useful even in that case.
		ddl, ddlSource, ddlErr := m.objectStudioDDL(ctx, source, schema, object, out.Type)
		out.DDL, out.DDLSource = ddl, ddlSource
		if ddlErr != nil {
			out.DDLError = ddlErr.Error()
		}
	}
	if out.DDL == "" {
		switch {
		case out.Index != nil && len(out.Index.Columns) > 0:
			cols := make([]string, len(out.Index.Columns))
			for i, column := range out.Index.Columns {
				cols[i] = quoteIdent(source.Kind, column)
			}
			unique := ""
			if strings.EqualFold(out.Index.Uniqueness, "UNIQUE") {
				unique = "UNIQUE "
			}
			out.DDL = "CREATE " + unique + "INDEX " + qualifiedStudioName(source.Kind, schema, out.Index.Name) + " ON " + qualifiedStudioName(source.Kind, schema, out.Index.Table) + " (" + strings.Join(cols, ", ") + ")"
			out.DDLSource = "reconstructed"
		case out.Sequence != nil:
			seq := ObjectStudioSequence{Name: out.Sequence.Name, StartWith: out.Sequence.StartWith, Increment: out.Sequence.Increment, MinValue: out.Sequence.MinValue, MaxValue: out.Sequence.MaxValue, Cache: out.Sequence.Cache, Cycle: out.Sequence.Cycle, Order: out.Sequence.Order}
			if plan, planErr := BuildObjectStudioPlan(source, ObjectStudioChange{Action: "create", ObjectType: "sequence", Schema: schema, Name: seq.Name, Sequence: &seq}); planErr == nil && len(plan.Statements) == 1 {
				out.DDL, out.DDLSource = plan.Statements[0], "reconstructed"
			}
		}
	}
	deps, depErr := m.ObjectDependencies(ctx, source, schema, object, objectType)
	if depErr == nil {
		out.Dependencies = deps
	}
	status, known, statusErr := m.ObjectStatus(ctx, source, schema, object, objectType)
	if statusErr == nil && known {
		out.Status, out.StatusKnown, out.Valid = status, true, strings.EqualFold(status, "VALID")
	}
	return out, nil
}

func (m *Manager) objectStudioDDL(ctx context.Context, source Source, schema, object, objectType string) (string, string, error) {
	typ := strings.ToUpper(strings.TrimSpace(objectType))
	if typ == "" {
		typ = "TABLE"
	}
	switch typ {
	case "TABLE", "VIEW", "MATERIALIZED VIEW", "MATERIALIZED_VIEW", "INDEX", "CONSTRAINT", "SEQUENCE":
	default:
		typ = "TABLE"
	}
	if typ == "MATERIALIZED_VIEW" {
		typ = "MATERIALIZED VIEW"
	}
	var ddl string
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var raw sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT DBMS_METADATA.GET_DDL(:1, :2, :3) FROM dual`, typ, strings.ToUpper(object), strings.ToUpper(schema)).Scan(&raw); err != nil {
			return err
		}
		ddl = strings.TrimSpace(raw.String)
		return nil
	})
	if err != nil {
		return "", "", err
	}
	if ddl == "" {
		return "", "", errors.New("对象没有可显示的 DDL")
	}
	return ddl, "dbms_metadata", nil
}

// ObjectStatus provides an inexpensive validity lookup.  Oracle exposes
// VALID/INVALID in ALL_OBJECTS.  MySQL has no equivalent object status, so the
// response is explicitly marked unknown rather than pretending valid.
func (m *Manager) ObjectStatus(ctx context.Context, source Source, schema, object, objectType string) (status string, known bool, err error) {
	if source.Kind != KindOracle {
		return "", false, nil
	}
	if err = validateStudioIdentifier("schema", schema, source.Kind); err != nil {
		return "", false, err
	}
	if err = validateStudioIdentifier("对象名", object, source.Kind); err != nil {
		return "", false, err
	}
	typ := strings.ToUpper(strings.TrimSpace(objectType))
	err = m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var value sql.NullString
		var query string
		var args []any
		if typ == "" {
			query = `SELECT status FROM all_objects WHERE owner = :1 AND object_name = :2 AND ROWNUM = 1`
			args = []any{strings.ToUpper(schema), strings.ToUpper(object)}
		} else {
			query = `SELECT status FROM all_objects WHERE owner = :1 AND object_name = :2 AND object_type = :3`
			args = []any{strings.ToUpper(schema), strings.ToUpper(object), typ}
		}
		err := db.QueryRowContext(ctx, query, args...).Scan(&value)
		if err == sql.ErrNoRows {
			return fmt.Errorf("对象不存在: %s.%s", schema, object)
		}
		if err != nil {
			return err
		}
		status = strings.ToUpper(strings.TrimSpace(value.String))
		known = status != ""
		return nil
	})
	return status, known, err
}

func (m *Manager) InvalidObjects(ctx context.Context, source Source, schema, search string) ([]InvalidObject, error) {
	if source.Kind != KindOracle {
		return []InvalidObject{}, nil
	}
	if schema != "" {
		if err := validateStudioIdentifier("schema", schema, source.Kind); err != nil {
			return nil, err
		}
	}
	if len(search) > 256 {
		return nil, errors.New("search 不能超过 256 字节")
	}
	var out []InvalidObject
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		owner := strings.ToUpper(strings.TrimSpace(schema))
		var rows *sql.Rows
		var err error
		like := "%" + strings.ToUpper(strings.TrimSpace(search)) + "%"
		switch {
		case owner == "" && strings.TrimSpace(search) == "":
			rows, err = db.QueryContext(ctx, `SELECT owner, object_name, object_type, status FROM all_objects WHERE status <> 'VALID' ORDER BY owner, object_type, object_name`)
		case owner == "":
			rows, err = db.QueryContext(ctx, `SELECT owner, object_name, object_type, status FROM all_objects WHERE status <> 'VALID' AND UPPER(object_name) LIKE :1 ORDER BY owner, object_type, object_name`, like)
		case strings.TrimSpace(search) == "":
			rows, err = db.QueryContext(ctx, `SELECT owner, object_name, object_type, status FROM all_objects WHERE status <> 'VALID' AND owner = :1 ORDER BY owner, object_type, object_name`, owner)
		default:
			rows, err = db.QueryContext(ctx, `SELECT owner, object_name, object_type, status FROM all_objects WHERE status <> 'VALID' AND owner = :1 AND UPPER(object_name) LIKE :2 ORDER BY owner, object_type, object_name`, owner, like)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() && len(out) < 1000 {
			var item InvalidObject
			if err := rows.Scan(&item.Schema, &item.Name, &item.Type, &item.Status); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (m *Manager) ObjectDependencies(ctx context.Context, source Source, schema, object, objectType string) ([]ObjectDependency, error) {
	if source.Kind != KindOracle && source.Kind != KindMySQL {
		return nil, errors.New("Redis 不支持对象依赖")
	}
	if err := validateStudioIdentifier("schema", schema, source.Kind); err != nil {
		return nil, err
	}
	if err := validateStudioIdentifier("对象名", object, source.Kind); err != nil {
		return nil, err
	}
	var out []ObjectDependency
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var rows *sql.Rows
		var err error
		if source.Kind == KindOracle {
			typ := strings.ToUpper(strings.TrimSpace(objectType))
			if typ == "" {
				rows, err = db.QueryContext(ctx, `SELECT owner, name, type, referenced_owner, referenced_name, referenced_type, dependency_type FROM all_dependencies WHERE owner = :1 AND name = :2 ORDER BY referenced_owner, referenced_name`, strings.ToUpper(schema), strings.ToUpper(object))
			} else {
				rows, err = db.QueryContext(ctx, `SELECT owner, name, type, referenced_owner, referenced_name, referenced_type, dependency_type FROM all_dependencies WHERE owner = :1 AND name = :2 AND type = :3 ORDER BY referenced_owner, referenced_name`, strings.ToUpper(schema), strings.ToUpper(object), typ)
			}
		} else {
			// MySQL exposes view dependencies in VIEW_TABLE_USAGE and foreign-key
			// dependencies in KEY_COLUMN_USAGE.  Return a unified object-level list.
			rows, err = db.QueryContext(ctx, `SELECT table_schema, table_name, 'VIEW', referenced_table_schema, referenced_table_name, 'TABLE', 'VIEW_TABLE_USAGE' FROM information_schema.view_table_usage WHERE view_schema = ? AND view_name = ? UNION ALL SELECT constraint_schema, table_name, 'TABLE', referenced_table_schema, referenced_table_name, 'TABLE', 'FOREIGN_KEY' FROM information_schema.key_column_usage WHERE constraint_schema = ? AND table_name = ? AND referenced_table_name IS NOT NULL ORDER BY referenced_table_schema, referenced_table_name`, schema, object, schema, object)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() && len(out) < 2000 {
			var item ObjectDependency
			var dependencyType sql.NullString
			if err := rows.Scan(&item.Schema, &item.Name, &item.Type, &item.ReferencedSchema, &item.ReferencedName, &item.ReferencedType, &dependencyType); err != nil {
				return err
			}
			item.DependencyType = dependencyType.String
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func oracleFunctionIdentifierError(schema, name string) error {
	if err := validateStudioIdentifier("schema", schema, KindOracle); err != nil {
		return err
	}
	return validateStudioIdentifier("函数名", name, KindOracle)
}

var oracleFunctionHeaderRE = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+(?:("(?:[^"]|"")+"|[A-Za-z_][A-Za-z0-9_$#]*)\s*\.\s*)?("(?:[^"]|"")+"|[A-Za-z_][A-Za-z0-9_$#]*)\s*(?:\(|RETURN\b)`)

var (
	oracleLineColumnRE  = regexp.MustCompile(`(?i)\bline\s+([0-9]+)\s*,\s*column\s+([0-9]+)`)
	oracleErrorNumberRE = regexp.MustCompile(`(?i)\b(?:ORA|PLS)-([0-9]+)\b`)
)

func parseOracleCompileError(schema, name, text string) OracleCompileError {
	item := OracleCompileError{Schema: schema, Name: name, Type: "FUNCTION", Text: strings.TrimSpace(text)}
	if match := oracleLineColumnRE.FindStringSubmatch(text); len(match) == 3 {
		item.Line, _ = strconv.Atoi(match[1])
		item.Column, _ = strconv.Atoi(match[2])
		item.Position = item.Column
	}
	if match := oracleErrorNumberRE.FindStringSubmatch(text); len(match) == 2 {
		item.ErrorNumber, _ = strconv.Atoi(match[1])
	}
	return item
}

func normalizeOracleFunctionSource(schema, name, source string, replace bool) (string, error) {
	if err := oracleFunctionIdentifierError(schema, name); err != nil {
		return "", err
	}
	source = strings.TrimSpace(strings.TrimPrefix(source, "\ufeff"))
	if source == "" || len(source) > maxObjectStudioSQL {
		return "", errors.New("函数源码不能为空且不能超过 1MB")
	}
	if strings.ContainsRune(source, '\x00') {
		return "", errors.New("函数源码包含 NUL 字符")
	}
	// SQL*Plus slash separators are client commands, not part of a statement.
	// Accept one final slash for convenient paste but reject any additional
	// statement separator.
	lines := strings.Split(source, "\n")
	slashLines := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "/" {
			slashLines++
			if i != len(lines)-1 && !(i == len(lines)-2 && strings.TrimSpace(lines[len(lines)-1]) == "") {
				return "", errors.New("函数源码只能包含末尾一个 / 分隔符")
			}
		}
	}
	if slashLines > 1 {
		return "", errors.New("函数源码只能包含一个 / 分隔符")
	}
	if slashLines == 1 {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "/" {
			lines = lines[:len(lines)-1]
		}
		source = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	match := oracleFunctionHeaderRE.FindStringSubmatch(source)
	unquote := func(value string) string {
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			return strings.ReplaceAll(value[1:len(value)-1], `""`, `"`)
		}
		return value
	}
	if len(match) != 3 || !strings.EqualFold(unquote(match[2]), name) {
		return "", fmt.Errorf("函数源码头部必须是 CREATE [OR REPLACE] FUNCTION %s", name)
	}
	// If a schema qualifier is present, ensure it names the requested schema.
	if match[1] != "" && !strings.EqualFold(unquote(match[1]), schema) {
		return "", errors.New("函数源码 schema 与请求 schema 不一致")
	}
	if replace && !regexp.MustCompile(`(?is)^\s*CREATE\s+OR\s+REPLACE\s+FUNCTION\b`).MatchString(source) {
		source = regexp.MustCompile(`(?is)^\s*CREATE\s+FUNCTION\b`).ReplaceAllString(source, "CREATE OR REPLACE FUNCTION")
	}
	if !strings.Contains(strings.ToUpper(source), "RETURN") {
		return "", errors.New("函数源码缺少 RETURN 声明")
	}
	return source, nil
}

// NormalizeOracleFunctionSource exposes the same source validation used by
// CompileOracleFunction for a preview-only request.
func NormalizeOracleFunctionSource(change OracleFunctionChange) (string, error) {
	return normalizeOracleFunctionSource(change.Schema, change.Name, change.Source, change.Replace)
}

func (m *Manager) LoadOracleFunction(ctx context.Context, source Source, schema, name string) (OracleFunction, error) {
	if source.Kind != KindOracle {
		return OracleFunction{}, errors.New("只有 Oracle 支持 Function 源码编辑")
	}
	if err := oracleFunctionIdentifierError(schema, name); err != nil {
		return OracleFunction{}, err
	}
	out := OracleFunction{Schema: schema, Name: name, Type: "FUNCTION", LoadedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	err := m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		var status sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT status FROM all_objects WHERE owner = :1 AND object_name = :2 AND object_type = 'FUNCTION'`, strings.ToUpper(schema), strings.ToUpper(name)).Scan(&status); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("函数不存在: %s.%s", schema, name)
			}
			return err
		}
		out.Status = strings.ToUpper(strings.TrimSpace(status.String))
		out.Valid = out.Status == "VALID"
		rows, err := db.QueryContext(ctx, `SELECT text FROM all_source WHERE owner = :1 AND name = :2 AND type = 'FUNCTION' ORDER BY line`, strings.ToUpper(schema), strings.ToUpper(name))
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
			if !strings.HasSuffix(line.String, "\n") {
				b.WriteByte('\n')
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		out.Source = strings.TrimSpace(b.String())
		errs, loadErr := loadOracleCompileErrors(ctx, db, schema, name, "FUNCTION")
		if loadErr != nil {
			return loadErr
		}
		out.Errors = errs
		return nil
	})
	return out, err
}

func loadOracleCompileErrors(ctx context.Context, db *sql.DB, schema, name, objectType string) ([]OracleCompileError, error) {
	rows, err := db.QueryContext(ctx, `SELECT owner, name, type, sequence, line, position, NVL(attribute,''), NVL(message_number,0), NVL(text,'') FROM all_errors WHERE owner = :1 AND name = :2 AND type = :3 ORDER BY sequence`, strings.ToUpper(schema), strings.ToUpper(name), strings.ToUpper(objectType))
	if err != nil {
		// Older/locked-down Oracle compatibility: MESSAGE_NUMBER may not be
		// selectable from an unusual proxy view.  Keep all text and positions.
		rows, err = db.QueryContext(ctx, `SELECT owner, name, type, sequence, line, position, NVL(attribute,''), NVL(text,'') FROM all_errors WHERE owner = :1 AND name = :2 AND type = :3 ORDER BY sequence`, strings.ToUpper(schema), strings.ToUpper(name), strings.ToUpper(objectType))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []OracleCompileError
		for rows.Next() {
			var item OracleCompileError
			if err := rows.Scan(&item.Schema, &item.Name, &item.Type, &item.Sequence, &item.Line, &item.Position, &item.Attribute, &item.Text); err != nil {
				return nil, err
			}
			item.Column = item.Position
			out = append(out, item)
		}
		return out, rows.Err()
	}
	defer rows.Close()
	var out []OracleCompileError
	for rows.Next() {
		var item OracleCompileError
		if err := rows.Scan(&item.Schema, &item.Name, &item.Type, &item.Sequence, &item.Line, &item.Position, &item.Attribute, &item.ErrorNumber, &item.Text); err != nil {
			return nil, err
		}
		item.Column = item.Position
		out = append(out, item)
	}
	return out, rows.Err()
}

// CompileOracleFunction validates and executes CREATE OR REPLACE FUNCTION,
// then reads ALL_ERRORS/ALL_OBJECTS in the same request.  A database compiler
// error is represented in the result (Success=false) instead of being turned
// into a transport error, allowing the editor to render line/column markers.
func (m *Manager) CompileOracleFunction(ctx context.Context, source Source, change OracleFunctionChange) (OracleFunctionCompileResult, error) {
	if source.Kind != KindOracle {
		return OracleFunctionCompileResult{}, errors.New("只有 Oracle 支持 Function 编译")
	}
	if !source.DDLAllowed() {
		return OracleFunctionCompileResult{}, errors.New("当前数据源未开启 DDL 能力（allow_ddl=true），或处于只读模式")
	}
	if source.IsProduction() && !change.Confirm {
		return OracleFunctionCompileResult{}, errors.New("生产数据源编译 Function 需要 confirm=true")
	}
	sqlText, err := normalizeOracleFunctionSource(change.Schema, change.Name, change.Source, change.Replace)
	if err != nil {
		return OracleFunctionCompileResult{}, err
	}
	result := OracleFunctionCompileResult{SQL: sqlText}
	started := time.Now()
	err = m.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		if _, execErr := db.ExecContext(ctx, sqlText); execErr != nil {
			if isConnectionFailure(execErr) || errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
				return execErr
			}
			result.ExecutionError = execErr.Error()
		}
		fn, loadErr := m.loadOracleFunctionOnDB(ctx, db, change.Schema, change.Name)
		if loadErr != nil {
			// A compiler error usually leaves the object in ALL_OBJECTS.  If it
			// was a brand-new function and no row exists, return the execution
			// error as a transport error so callers can distinguish connectivity.
			if result.ExecutionError != "" {
				return nil
			}
			return loadErr
		}
		result.Function = fn
		result.Errors = fn.Errors
		result.Compiled = true
		result.Success = result.ExecutionError == "" && fn.Valid && len(fn.Errors) == 0
		return nil
	})
	result.ElapsedMS = time.Since(started).Milliseconds()
	if err != nil {
		return result, err
	}
	if result.ExecutionError != "" && len(result.Errors) == 0 {
		// Keep the error in a line-oriented entry when Oracle did not expose
		// ALL_ERRORS, which still lets the editor show a useful diagnostic.
		result.Errors = []OracleCompileError{parseOracleCompileError(change.Schema, change.Name, result.ExecutionError)}
	}
	m.InvalidateMetadata(source.ID)
	return result, nil
}

func (m *Manager) LoadFunction(ctx context.Context, source Source, schema, name string) (OracleFunction, error) {
	return m.LoadOracleFunction(ctx, source, schema, name)
}

func (m *Manager) CompileFunction(ctx context.Context, source Source, change OracleFunctionChange) (OracleFunctionCompileResult, error) {
	return m.CompileOracleFunction(ctx, source, change)
}

func (m *Manager) loadOracleFunctionOnDB(ctx context.Context, db *sql.DB, schema, name string) (OracleFunction, error) {
	out := OracleFunction{Schema: schema, Name: name, Type: "FUNCTION", LoadedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	var status sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT status FROM all_objects WHERE owner = :1 AND object_name = :2 AND object_type = 'FUNCTION'`, strings.ToUpper(schema), strings.ToUpper(name)).Scan(&status); err != nil {
		return out, err
	}
	out.Status = strings.ToUpper(strings.TrimSpace(status.String))
	out.Valid = out.Status == "VALID"
	rows, err := db.QueryContext(ctx, `SELECT text FROM all_source WHERE owner = :1 AND name = :2 AND type = 'FUNCTION' ORDER BY line`, strings.ToUpper(schema), strings.ToUpper(name))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line sql.NullString
		if err := rows.Scan(&line); err != nil {
			return out, err
		}
		b.WriteString(line.String)
		if !strings.HasSuffix(line.String, "\n") {
			b.WriteByte('\n')
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Source = strings.TrimSpace(b.String())
	out.Errors, err = loadOracleCompileErrors(ctx, db, schema, name, "FUNCTION")
	return out, err
}
