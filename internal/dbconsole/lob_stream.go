package dbconsole

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// LOBRef 描述一次 LOB 点击下载所需的行定位信息。
// 结论 2.3 优先级：主键 > 全部 NOT NULL 唯一键 > 单表 ROWID。
// 为安全，必须经数据字典校验表与列真实存在且列确为 LOB 类型，全部值走绑定变量。
type LOBRef struct {
	Owner      string         `json:"owner"`
	Table      string         `json:"table"`
	Column     string         `json:"column"`
	ColumnType string         `json:"column_type,omitempty"`
	RowID      string         `json:"rowid,omitempty"`
	UseRowID   bool           `json:"use_rowid,omitempty"`
	Keys       map[string]any `json:"keys,omitempty"`        // pk 列->值
	PrimaryKey []string       `json:"primary_key,omitempty"` // 供 SQL 生成
	SessionID  string         `json:"session_id,omitempty"`  // 若页签有未提交事务，固定到同一会话
}

func (r LOBRef) Validate() error {
	if strings.TrimSpace(r.Owner) == "" || strings.TrimSpace(r.Table) == "" || strings.TrimSpace(r.Column) == "" {
		return errors.New("LOB 引用缺少 owner/table/column")
	}
	if r.UseRowID {
		if strings.TrimSpace(r.RowID) == "" {
			return errors.New("ROWID 为空")
		}
		if strings.ContainsAny(r.RowID, "\x00\r\n'\"") {
			return errors.New("ROWID 非法")
		}
		return nil
	}
	if len(r.PrimaryKey) == 0 || len(r.Keys) == 0 {
		return errors.New("LOB 引用缺少主键信息")
	}
	for _, pk := range r.PrimaryKey {
		if _, ok := r.Keys[pk]; !ok {
			// 允许大小写差异
			found := false
			for k := range r.Keys {
				if strings.EqualFold(k, pk) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("缺少主键值 %s", pk)
			}
		}
	}
	return nil
}

// WhereClause 生成 WHERE 子句与绑定参数（全绑定变量，无拼接字面量）。
func (r LOBRef) WhereClause() (string, []any, error) {
	if err := r.Validate(); err != nil {
		return "", nil, err
	}
	if r.UseRowID {
		return "ROWID = :kairo_rowid", []any{sql.Named("kairo_rowid", r.RowID)}, nil
	}
	parts := make([]string, 0, len(r.PrimaryKey))
	args := make([]any, 0, len(r.PrimaryKey))
	for i, pk := range r.PrimaryKey {
		col, err := quoteGridIdentifier(KindOracle, pk, "主键列")
		if err != nil {
			return "", nil, err
		}
		val, ok := r.Keys[pk]
		if !ok {
			for k, v := range r.Keys {
				if strings.EqualFold(k, pk) {
					val = v
					ok = true
					break
				}
			}
		}
		if !ok {
			return "", nil, fmt.Errorf("缺少主键值 %s", pk)
		}
		if val == nil {
			parts = append(parts, col+" IS NULL")
			continue
		}
		marker := fmt.Sprintf(":kairo_pk%d", i+1)
		parts = append(parts, col+" = "+marker)
		args = append(args, sql.Named(fmt.Sprintf("kairo_pk%d", i+1), val))
	}
	if len(parts) == 0 {
		return "", nil, errors.New("主键条件不能为空")
	}
	return strings.Join(parts, " AND "), args, nil
}

// StreamLOB 企业版点击加载入口：路由到对应 OracleBackend 执行流式写入 dst。
func (m *Manager) StreamLOB(ctx context.Context, source Source, ref LOBRef, dst io.Writer) error {
	backend := m.ResolveOracleBackend(source)
	if backend != nil {
		return backend.StreamLOB(ctx, m, source, ref, dst)
	}
	return m.streamLOBChunked(ctx, source, ref, dst)
}

// streamLOBChunked 分块安全读取实现（基于 DBMS_LOB.SUBSTR，兼容 go-ora 与任意 Session/事务场景）
func (m *Manager) streamLOBChunked(ctx context.Context, source Source, ref LOBRef, dst io.Writer) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: LOB 流 panic 已捕获（连接已销毁）: %v", driver.ErrBadConn, r)
			if source.ID != "" {
				m.invalidatePool(source.ID)
			}
		} else if err != nil && isTTCError(err) {
			err = mapTTCError(err)
			m.invalidatePool(source.ID)
		}
	}()
	return m.withLOBQuery(ctx, source, ref, func(ctx context.Context, q lobQueryer) error {
		dst = &lobLimitWriter{ctx: ctx, dst: dst}
		colType, err := validateLOBColumn(ctx, q, ref)
		if err != nil {
			return err
		}
		whereSQL, args, err := ref.WhereClause()
		if err != nil {
			return err
		}
		tableSQL, err := quoteGridIdentifier(KindOracle, ref.Table, "表名")
		if err != nil {
			return err
		}
		ownerSQL := ""
		if strings.TrimSpace(ref.Owner) != "" {
			oq, e2 := quoteGridIdentifier(KindOracle, ref.Owner, "schema")
			if e2 != nil {
				return e2
			}
			ownerSQL = oq + "."
		}
		colSQL, err := quoteGridIdentifier(KindOracle, ref.Column, "列名")
		if err != nil {
			return err
		}
		// 2. 取长度做上限校验（GETLENGTH：CLOB 按字符，BLOB 按字节）
		var totalLen sql.NullInt64
		lenSQL := fmt.Sprintf("SELECT DBMS_LOB.GETLENGTH(%s) FROM %s%s WHERE %s", colSQL, ownerSQL, tableSQL, whereSQL)
		var lenErr error
		lenErr = q.QueryRowContext(ctx, lenSQL, args...).Scan(&totalLen)
		if lenErr != nil {
			err = lenErr
			if isConnectionFailure(err) || isTTCError(err) {
				m.invalidatePool(source.ID)
				err = driver.ErrBadConn
			}
			return mapTTCError(err)
		}
		if !totalLen.Valid || totalLen.Int64 == 0 {
			return nil // NULL / EMPTY_LOB：零字节流，合法
		}
		const maxStreamBytes = 64 << 20
		estBytes := totalLen.Int64
		if !isBlobType(colType) {
			estBytes *= 4 // CLOB 字符→字节最坏（AL32UTF8 4B/char）
		}
		if estBytes > maxStreamBytes {
			return fmt.Errorf("LOB 过大（约 %d 字节），超过 %d MB 上限，请缩小范围或联系管理员", estBytes, maxStreamBytes>>20)
		}
		// 3. 分块拉取：ctx 取消即停，每块写透 dst（支持 HTTP 背压）
		const clobChunk = 500  // Fits Oracle 11g SQL VARCHAR2 after character conversion.
		const blobChunk = 2000 // Oracle 11g SQL RAW limit.
		var offset int64 = 1
		unit := totalLen.Int64
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			if offset > unit {
				break
			}
			chunkSize := int64(clobChunk)
			if isBlobType(colType) {
				chunkSize = blobChunk
			}
			if remain := unit - offset + 1; remain < chunkSize {
				chunkSize = remain
			}
			chunkSQL := fmt.Sprintf("SELECT DBMS_LOB.SUBSTR(%s, :kairo_amt, :kairo_off) FROM %s%s WHERE %s", colSQL, ownerSQL, tableSQL, whereSQL)
			chunkArgs := append([]any{sql.Named("kairo_amt", chunkSize), sql.Named("kairo_off", offset)}, args...)
			if isBlobType(colType) {
				var b []byte
				var scanErr error
				scanErr = q.QueryRowContext(ctx, chunkSQL, chunkArgs...).Scan(&b)
				if scanErr != nil {
					err = scanErr
					if isConnectionFailure(err) || isTTCError(err) {
						m.invalidatePool(source.ID)
						err = driver.ErrBadConn
					}
					return mapTTCError(err)
				}
				if len(b) == 0 {
					return io.ErrUnexpectedEOF
				}
				if _, err := dst.Write(b); err != nil {
					return err
				}
				offset += int64(len(b))
			} else {
				var s sql.NullString
				var scanErr2 error
				scanErr2 = q.QueryRowContext(ctx, chunkSQL, chunkArgs...).Scan(&s)
				if scanErr2 != nil {
					err = scanErr2
					if isConnectionFailure(err) || isTTCError(err) {
						m.invalidatePool(source.ID)
						err = driver.ErrBadConn
					}
					return mapTTCError(err)
				}
				if !s.Valid || s.String == "" {
					return io.ErrUnexpectedEOF
				}
				if _, err := io.WriteString(dst, s.String); err != nil {
					return err
				}
				offInc := int64(len(utf16.Encode([]rune(s.String))))
				if offInc == 0 {
					offInc = chunkSize
				}
				offset += offInc

			}
		}
		return nil
	})
}

func (m *Manager) oracleColumnType(ctx context.Context, db *sql.DB, owner, table, column string) (string, error) {
	var t string
	err := db.QueryRowContext(ctx, `SELECT data_type FROM all_tab_columns WHERE owner = :1 AND table_name = :2 AND column_name = :3`, strings.ToUpper(owner), strings.ToUpper(table), strings.ToUpper(column)).Scan(&t)
	if err != nil {
		return "", fmt.Errorf("校验 LOB 列失败（请确认表与列存在且有权限）：%w", err)
	}
	return strings.ToUpper(strings.TrimSpace(t)), nil
}

func (m *Manager) oracleColumnTypeTx(ctx context.Context, tx *sql.Tx, owner, table, column string) (string, error) {
	var t string
	err := tx.QueryRowContext(ctx, `SELECT data_type FROM all_tab_columns WHERE owner = :1 AND table_name = :2 AND column_name = :3`, strings.ToUpper(owner), strings.ToUpper(table), strings.ToUpper(column)).Scan(&t)
	if err != nil {
		return "", fmt.Errorf("校验 LOB 列失败（请确认表与列存在且有权限）：%w", err)
	}
	return strings.ToUpper(strings.TrimSpace(t)), nil
}

func isLOBType(t string) bool {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case "CLOB", "NCLOB", "BLOB":
		return true
	}
	if strings.Contains(strings.ToUpper(t), "CLOB") || strings.Contains(strings.ToUpper(t), "BLOB") {
		return true
	}
	return false
}

func isBlobType(t string) bool {
	ut := strings.ToUpper(strings.TrimSpace(t))
	return ut == "BLOB" || ut == "LONG RAW" || ut == "RAW" || ut == "BFILE" || strings.HasSuffix(ut, "BLOB")
}

func isTTCError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToUpper(err.Error())
	return strings.Contains(text, "TTC ERROR") || strings.Contains(text, "BAD TTC PACKET") || strings.Contains(text, "RECEIVED CODE")
}

func mapTTCError(err error) error {
	if err == nil {
		return nil
	}
	if isTTCError(err) {
		return fmt.Errorf("%w: %v", driver.ErrBadConn, err)
	}
	// 额外：所有 TTC 解析错误销毁物理连接，绝不放回池
	if isConnectionFailure(err) {
		return err
	}
	return err
}
