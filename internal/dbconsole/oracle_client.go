package dbconsole

import (
	"context"
	"fmt"
)

// OracleClientInfo 企业版 OCI 自检与客户端探测详情
type OracleClientInfo struct {
	Backend       string                  `json:"backend"`
	Available     bool                    `json:"available"`
	Bitness       string                  `json:"bitness"`
	LibDir        string                  `json:"lib_dir,omitempty"`
	ConfigDir     string                  `json:"config_dir,omitempty"`
	ClientVersion string                  `json:"client_version,omitempty"`
	Detail        string                  `json:"detail,omitempty"`
	Candidates    []OracleCandidateClient `json:"candidates,omitempty"`
	PLSQLDetected bool                    `json:"plsql_detected,omitempty"`
	PLSQLBitness  string                  `json:"plsql_bitness,omitempty"`
	PLSQLDetail   string                  `json:"plsql_detail,omitempty"`
}

func (m *Manager) OracleClientInfo(ctx context.Context, sourceID string) (OracleClientInfo, error) {
	var src Source
	if sourceID != "" {
		if s, ok := m.Store().Get(sourceID); ok && s.Kind == KindOracle {
			src = s
		} else {
			return OracleClientInfo{}, fmt.Errorf("Oracle 数据源不存在")
		}
	}
	backend := m.ResolveOracleBackend(src)
	info, err := backend.ClientInfo(ctx, src)
	if err != nil {
		return OracleClientInfo{Backend: "unknown", Available: false, Detail: fmt.Sprintf("探测失败: %v", err)}, err
	}
	if src.ID != "" && backend.Name() == "godror" {
		ctx, cancel := context.WithTimeout(ctx, src.Timeout())
		defer cancel()
		db, openErr := m.sqlDB(src)
		if openErr == nil {
			openErr = db.PingContext(ctx)
		}
		if openErr != nil {
			info.Available = false
			info.Detail = fmt.Sprintf("OCI 连接验证失败: %v", openErr)
			return info, nil
		}
		info.ClientVersion, err = oracleLoadedClientVersion(ctx, db)
		if err != nil {
			return info, err
		}
		info.Detail = "OCI 已成功加载，客户端版本 " + info.ClientVersion
	}
	return info, nil
}
