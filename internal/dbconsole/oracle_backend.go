package dbconsole

import (
	"context"
	"crypto/tls"
	"database/sql/driver"
	"io"
	"strings"
)

// OracleCapabilities 描述 Oracle 后端驱动的技术能力边界
type OracleCapabilities struct {
	NativeOCI       bool `json:"native_oci"`        // 是否通过 OCI / ODPI-C 原生客户端运行
	DirectLOB       bool `json:"direct_lob"`        // 是否支持驱动层原生定位器流式拉取
	SSHTunnelDialer bool `json:"ssh_tunnel_dialer"` // 是否支持 Go 层 SSH 隧道 DialContext 注入
	PureGo          bool `json:"pure_go"`           // 是否无需任何 C 编译或 DLL 依赖
	Supports11g     bool `json:"supports_11g"`      // 是否支持 Oracle 11g
}

// OracleBackend 统一的 Oracle 驱动后端接口
type OracleBackend interface {
	Name() string
	Available() bool
	Capabilities() OracleCapabilities
	ClientInfo(ctx context.Context, source Source) (OracleClientInfo, error)
	OpenConnector(source Source, password string, dialer funcDialer, hasDialer bool, tlsConfig *tls.Config, targetHost string, targetPort int) (driver.Connector, string, error)
	StreamLOB(ctx context.Context, m *Manager, source Source, ref LOBRef, dst io.Writer) error
}

// ResolveOracleBackend 根据数据源配置、网络拓扑与本地客户端环境智能裁决最佳后端。
// 决策规则（结论 1.4/3.1/3.2）：
//  1. SSH 隧道场景：ODPI-C 无法注入 Go Dialer，必须走 go-ora (纯 Go 隧道拨号)。
//  2. 显式配置：若 source.OracleDriver 指定 "go-ora"，走 go-ora；若指定 "godror"，走 godror。
//  3. 默认自动策略：Windows 企业环境优先 godror/OCI；若机器未检测到兼容的 64 位 OCI（例如仅有 32 位 PL/SQL Client），
//     自动平滑降级为 go-ora，保证连接始终可用。
func (m *Manager) ResolveOracleBackend(source Source) OracleBackend {
	hasTunnel := source.SSHTunnel != nil && source.SSHTunnel.Enabled
	driverChoice := strings.ToLower(strings.TrimSpace(source.OracleDriver))

	godrorBackend := newGodrorBackend()
	if driverChoice == "godror" {
		return godrorBackend
	}
	if hasTunnel || driverChoice == "go-ora" {
		return &GoOraBackend{}
	}

	// 自动模式：检查 godror 是否可用（构建标签启用 CGO 且本地有兼容 64 位 OCI Client）
	if godrorBackend.Capabilities().NativeOCI {
		cands, _, _, _ := DetectSystemOracleClients(source)
		if best := SelectBestCandidate(cands, source.OracleLibDir); best != nil && best.Compatible {
			return godrorBackend
		}
	}
	return &GoOraBackend{}
}

// GoOraBackend 纯 Go (go-ora/v2) 后端实现（便携版、应急与降级能力）
type GoOraBackend struct{}

func (b *GoOraBackend) Name() string { return "go-ora" }

func (b *GoOraBackend) Available() bool { return true }

func (b *GoOraBackend) Capabilities() OracleCapabilities {
	return OracleCapabilities{
		NativeOCI:       false,
		DirectLOB:       false,
		SSHTunnelDialer: true,
		PureGo:          true,
		Supports11g:     true,
	}
}

func (b *GoOraBackend) ClientInfo(ctx context.Context, source Source) (OracleClientInfo, error) {
	cands, plsqlDetected, plsqlBitness, plsqlDetail := DetectSystemOracleClients(source)
	info := OracleClientInfo{
		Backend:       "go-ora",
		Available:     true,
		Bitness:       bitnessStub(),
		Candidates:    cands,
		PLSQLDetected: plsqlDetected,
		PLSQLBitness:  plsqlBitness,
		PLSQLDetail:   plsqlDetail,
		Detail:        "当前使用纯 Go go-ora thin 驱动（无需 Oracle Instant Client，支持 SSH 隧道）。",
	}
	best := SelectBestCandidate(cands, source.OracleLibDir)
	if best != nil {
		info.LibDir = best.Path
		info.ConfigDir = best.ConfigDir
	}
	return info, nil
}

func (b *GoOraBackend) OpenConnector(source Source, password string, dialer funcDialer, _ bool, tlsConfig *tls.Config, targetHost string, targetPort int) (driver.Connector, string, error) {
	return openOracleViaGoOraStub(source, password, dialer, tlsConfig, targetHost, targetPort)
}

func (b *GoOraBackend) StreamLOB(ctx context.Context, m *Manager, source Source, ref LOBRef, dst io.Writer) error {
	// go-ora 后端通过受控分块拉取（DBMS_LOB.SUBSTR），不走 OCI 定位器
	return m.streamLOBChunked(ctx, source, ref, dst)
}

// 确保 GoOraBackend 满足 OracleBackend
var _ OracleBackend = (*GoOraBackend)(nil)

func (m *Manager) OracleBackendFor(source Source) OracleBackend {
	return m.ResolveOracleBackend(source)
}

func (m *Manager) OracleBackendName(source Source) string {
	backend := m.ResolveOracleBackend(source)
	if backend != nil {
		return backend.Name()
	}
	return "go-ora"
}
