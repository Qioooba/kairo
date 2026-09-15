//go:build !cgo

package dbconsole

import (
	"context"
	"crypto/tls"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"runtime"
	"strings"
)

func oracleOpenResult(source Source, password string, dialer funcDialer, _ bool, tlsConfig *tls.Config, targetHost string, targetPort int) (driver.Connector, string, error) {
	return nil, "", fmt.Errorf("此便携版未编译 OCI 支持，请使用企业版或选择 go-ora 驱动")
}

// GodrorBackendStub 在纯 Go 构建下的占位
type GodrorBackendStub struct{}

func oracleLoadedClientVersion(ctx context.Context, db *sql.DB) (string, error) {
	return "", fmt.Errorf("此构建未启用 OCI")
}

func oracleGridArgs(args []any) []any                            { return args }
func oracleLOBPreview(src any, dbType string) (any, bool, error) { return nil, false, nil }

func (b *GodrorBackendStub) Name() string { return "godror" }

func (b *GodrorBackendStub) Available() bool { return false }

func (b *GodrorBackendStub) Capabilities() OracleCapabilities {
	return OracleCapabilities{
		NativeOCI:       false,
		DirectLOB:       false,
		SSHTunnelDialer: false,
		PureGo:          false,
		Supports11g:     true,
	}
}

func (b *GodrorBackendStub) ClientInfo(ctx context.Context, source Source) (OracleClientInfo, error) {
	return oracleClientInfoImpl(ctx, source)
}

func (b *GodrorBackendStub) OpenConnector(source Source, password string, dialer funcDialer, hasDialer bool, tlsConfig *tls.Config, targetHost string, targetPort int) (driver.Connector, string, error) {
	return oracleOpenResult(source, password, dialer, hasDialer, tlsConfig, targetHost, targetPort)
}

func (b *GodrorBackendStub) StreamLOB(ctx context.Context, m *Manager, source Source, ref LOBRef, dst io.Writer) error {
	return m.streamLOBChunked(ctx, source, ref, dst)
}

func newGodrorBackend() OracleBackend {
	return &GodrorBackendStub{}
}

func oracleClientInfoImpl(ctx context.Context, source Source) (OracleClientInfo, error) {
	cands, plsqlFound, plsqlBit, plsqlDetail := DetectSystemOracleClients(source)
	best := SelectBestCandidate(cands, source.OracleLibDir)

	detail := "当前为 CGO_ENABLED=0 构建（纯 Go go-ora 便携版，无需本地 Oracle Client）。"
	if plsqlFound {
		detail += " " + plsqlDetail
	}
	if best != nil {
		detail += fmt.Sprintf(" 检测到本地可用 Oracle Client: %s (%s)。若需开启 OCI 原生加速，请使用 CGO_ENABLED=1 的企业版构建。", best.Path, best.Bitness)
	} else if len(cands) > 0 {
		detail += fmt.Sprintf(" 检测到本地 %d 个 Oracle 候选路径，但未找到与当前架构 (%s) 完全匹配的 64 位完整客户端。", len(cands), runtime.GOARCH)
	}

	backendName := "go-ora"
	available := true
	if strings.ToLower(strings.TrimSpace(source.OracleDriver)) == "godror" {
		backendName = "godror"
		available = false
	}

	info := OracleClientInfo{
		Backend:       backendName,
		Available:     available,
		Bitness:       bitnessStub(),
		Candidates:    cands,
		PLSQLDetected: plsqlFound,
		PLSQLBitness:  plsqlBit,
		PLSQLDetail:   plsqlDetail,
		Detail:        detail,
	}
	if best != nil {
		info.LibDir = best.Path
		info.ConfigDir = best.ConfigDir
	}
	return info, nil
}
