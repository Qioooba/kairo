package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"doubao-toolbox/internal/diagnostics"
)

// handleDiagnostics GET /api/diagnostics
//
// 返回 Report JSON（diagnostics.Report），前端做表格 / 卡片展示。
// 不暴露任何凭据，只看配置 + DNS + TCP 可达性 + 工具存在性。
//
// Query:
//   - check_servers=false  跳过 server 检查（CI / 不联网环境加速）
//   - timeout_ms=N         每台 server 检查超时（默认 3000ms）
func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	q := r.URL.Query()
	opts := diagnostics.Options{
		CheckServers: !strings.EqualFold(q.Get("check_servers"), "false"),
	}
	if ms := q.Get("timeout_ms"); ms != "" {
		if n, err := json.Number(ms).Int64(); err == nil && n > 0 {
			opts.PerServerTimeout = time.Duration(n) * time.Millisecond
		}
	}

	cur := s.cur()
	rep := diagnostics.Collect(cur, s.cfg.Path(), opts)

	writeJSON(w, 200, rep)
}
