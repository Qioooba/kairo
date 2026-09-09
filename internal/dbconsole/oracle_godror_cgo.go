//go:build cgo

package dbconsole

import (
	"context"
	"crypto/tls"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/godror/godror"
	"github.com/godror/godror/dsn"
)

// GodrorBackend CGO 下的企业版 godror/OCI 实现（Windows 企业版默认驱动）
type GodrorBackend struct{}

func oracleLoadedClientVersion(ctx context.Context, db *sql.DB) (string, error) {
	version, err := godror.ClientVersion(ctx, db)
	return version.String(), err
}

func oracleGridArgs(args []any) []any {
	return append(append([]any(nil), args...), godror.LobAsReader(), godror.FetchArraySize(200))
}

func oracleLOBPreview(src any, dbType string) (any, bool, error) {
	lob, ok := src.(*godror.Lob)
	if !ok {
		return nil, false, nil
	}
	if lob == nil || lob.Reader == nil {
		return nil, true, nil
	}
	// Query locators are owned by Rows/Stmt; releasing the borrowed locator
	// here would make statement cleanup release the same native handle twice.
	buf, err := io.ReadAll(io.LimitReader(lob.Reader, maxLOBPreviewBytes+1))
	if err != nil {
		return nil, true, err
	}
	value := normalizeColumnValue(buf, dbType)
	return value, true, nil
}

func (b *GodrorBackend) Name() string { return "godror" }

func (b *GodrorBackend) Available() bool {
	cands, _, _, _ := DetectSystemOracleClients(Source{})
	best := SelectBestCandidate(cands, "")
	return best != nil && best.Compatible
}

func (b *GodrorBackend) Capabilities() OracleCapabilities {
	return OracleCapabilities{
		NativeOCI:       true,
		DirectLOB:       true,
		SSHTunnelDialer: false,
		PureGo:          false,
		Supports11g:     true,
	}
}

func (b *GodrorBackend) ClientInfo(ctx context.Context, source Source) (OracleClientInfo, error) {
	return oracleClientInfoImpl(ctx, source)
}

func (b *GodrorBackend) OpenConnector(source Source, password string, dialer funcDialer, hasDialer bool, tlsConfig *tls.Config, targetHost string, targetPort int) (driver.Connector, string, error) {
	return oracleOpenResult(source, password, dialer, hasDialer, tlsConfig, targetHost, targetPort)
}

func (b *GodrorBackend) StreamLOB(ctx context.Context, m *Manager, source Source, ref LOBRef, dst io.Writer) error {
	return m.streamLOBGodrorNative(ctx, source, ref, dst)
}

func newGodrorBackend() OracleBackend {
	return &GodrorBackend{}
}

// oracleOpenResult 裁决并创建 Connector：
//  1. SSH 隧道场景：ODPI-C 无法注入 Go Dialer，必须回落到 go-ora。
//  2. Windows 企业版：默认 godror/OCI。若检测到无可用 64 位 OCI（例如用户机器仅有 32 位 PL/SQL Client），
//     自动平滑回落到 go-ora，绝不让程序因 DPI-1047 崩溃。
func oracleOpenResult(source Source, password string, tunnelDialer funcDialer, hasDialer bool, tlsConfig *tls.Config, targetHost string, targetPort int) (driver.Connector, string, error) {
	if hasDialer {
		return nil, "", fmt.Errorf("OCI 不支持当前 SSH 隧道拨号器，请明确选择 go-ora")
	}

	cands, _, _, _ := DetectSystemOracleClients(source)
	best := SelectBestCandidate(cands, source.OracleLibDir)

	// 若未检测到任何兼容的 64 位 Oracle Client
	if best == nil || !best.Compatible {
		return nil, "", fmt.Errorf("未找到与当前 %s 程序匹配的 Oracle Client（oci.dll），请安装对应客户端或明确选择 go-ora", bitness())
	}

	params, dsnStr, err := buildGodrorParamsWithCandidate(source, password, targetHost, targetPort, tlsConfig, best)
	if err != nil {
		return nil, "", err
	}
	connector := godror.NewConnector(params)
	return connector, dsnStr, nil
}

func buildGodrorParamsWithCandidate(source Source, password string, targetHost string, targetPort int, tlsConfig *tls.Config, best *OracleCandidateClient) (dsn.ConnectionParams, string, error) {
	if tlsConfig != nil || (source.TLSMode != "" && source.TLSMode != "disabled") {
		return dsn.ConnectionParams{}, "", fmt.Errorf("OCI 尚未配置 Wallet/TCPS，不能忽略 TLS 设置；请选择 go-ora 或配置 Oracle Net 加密")
	}
	if strings.ContainsAny(targetHost+source.OracleService, "()\r\n\x00") {
		return dsn.ConnectionParams{}, "", fmt.Errorf("Oracle 连接地址包含非法描述符字符")
	}
	hostPort := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
	connectString := hostPort + "/" + strings.TrimSpace(source.OracleService)
	if source.OracleConnectBy == "sid" && strings.TrimSpace(source.OracleService) != "" {
		connectString = fmt.Sprintf("(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=%s)(PORT=%d))(CONNECT_DATA=(SID=%s)))", targetHost, targetPort, strings.TrimSpace(source.OracleService))
	}

	libDir := ""
	configDir := ""
	if best != nil {
		libDir = best.Path
		configDir = best.ConfigDir
	}
	if strings.TrimSpace(source.OracleConfigDir) != "" {
		configDir = source.OracleConfigDir
	}

	common := dsn.CommonParams{
		CommonSimpleParams: dsn.CommonSimpleParams{
			Username:      source.Username,
			Password:      dsn.NewPassword(password),
			ConnectString: connectString,
			LibDir:        libDir,
			ConfigDir:     configDir,
		},
	}
	if strings.EqualFold(strings.TrimSpace(source.OracleClientCharset), "ZHS16GBK") {
		common.Charset = "ZHS16GBK"
	}

	pool := dsn.PoolParams{
		MinSessions:      1,
		MaxSessions:      source.MaxOpenConnections,
		SessionIncrement: 1,
		WaitTimeout:      10 * time.Second,
		MaxLifeTime:      time.Duration(source.ConnectionMaxMinutes) * time.Minute,
		SessionTimeout:   5 * time.Minute,
	}

	params, err := dsn.Parse("")
	if err != nil {
		return params, "", err
	}
	params.CommonParams = common
	params.PoolParams = pool
	params.Timezone = time.Local
	if params.MaxSessions < 1 {
		params.MaxSessions = 4
	}
	_ = tlsConfig
	dsnStr := params.StringWithPassword()
	return params, dsnStr, nil
}

func oracleClientInfoImpl(ctx context.Context, source Source) (OracleClientInfo, error) {
	cands, plsqlDetected, plsqlBitness, plsqlDetail := DetectSystemOracleClients(source)
	best := SelectBestCandidate(cands, source.OracleLibDir)

	info := OracleClientInfo{
		Backend:       "godror",
		Bitness:       bitness(),
		Available:     false,
		Candidates:    cands,
		PLSQLDetected: plsqlDetected,
		PLSQLBitness:  plsqlBitness,
		PLSQLDetail:   plsqlDetail,
	}

	if best == nil || !best.Compatible {
		detail := "未找到与当前程序 (" + info.Bitness + ") 匹配的 Oracle Client（oci.dll）。"
		if plsqlDetected {
			detail += " " + plsqlDetail
		}
		detail += " 当前已自动平滑降级为纯 Go (go-ora) 驱动保障连接正常。"
		info.Detail = detail
		info.Backend = "go-ora"
		return info, nil
	}

	info.LibDir = best.Path
	info.ConfigDir = best.ConfigDir

	info.Available = true
	info.Detail = fmt.Sprintf("已发现匹配的 Oracle Client（%s），尚未连接验证，libDir=%s", best.Bitness, info.LibDir)
	return info, nil
}

func bitness() string {
	if strings.Contains(runtime.GOARCH, "64") {
		return "64-bit"
	}
	if strings.Contains(runtime.GOARCH, "386") {
		return "32-bit"
	}
	return runtime.GOARCH
}

// streamLOBGodrorNative 结论 3.3: 使用 godror.LobAsReader() 单列流式加载 LOB
func (m *Manager) streamLOBGodrorNative(ctx context.Context, source Source, ref LOBRef, dst io.Writer) error {
	return m.withLOBQuery(ctx, source, ref, func(ctx context.Context, q lobQueryer) error {
		if _, err := validateLOBColumn(ctx, q, ref); err != nil {
			return err
		}
		dst = &lobLimitWriter{ctx: ctx, dst: dst}
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

		query := fmt.Sprintf("SELECT %s FROM %s%s WHERE %s", colSQL, ownerSQL, tableSQL, whereSQL)

		queryArgs := append([]any(nil), args...)
		queryArgs = append(queryArgs, godror.LobAsReader(), godror.FetchArraySize(1), godror.CallTimeout(5*time.Second))

		// LOB readers borrow handles from Rows. A cancellable Rows context would
		// close them on another goroutine while OCI is still reading. Bound each
		// OCI round trip and cancel cooperatively between reads instead.
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := q.QueryContext(oracleCursorContext(ctx), query, queryArgs...)
		if err != nil {
			return fmt.Errorf("godror LOB query: %w", err)
		}
		defer rows.Close()

		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return fmt.Errorf("godror read LOB row: %w", err)
			}
			return sql.ErrNoRows
		}

		var lob *godror.Lob
		if err := rows.Scan(&lob); err != nil {
			return fmt.Errorf("godror scan LOB: %w", err)
		}

		if lob == nil || lob.Reader == nil {
			return nil
		}

		if _, err := io.CopyBuffer(dst, lobContextReader{ctx: ctx, reader: lob.Reader}, make([]byte, 64<<10)); err != nil {
			return fmt.Errorf("godror stream LOB: %w", err)
		}

		return nil
	})
}
