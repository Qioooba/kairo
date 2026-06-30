// Package httpserver - license 相关端点
//
// 端点清单:
//   GET  /api/license/status    前端查询 license 状态
//   POST /api/license/activate  前端提交激活码, Go 端转发到 Java 服务
package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"kairo/internal/license"
)

// handleLicenseStatus GET /api/license/status
//
// 返回 {licensed: bool, reason: string} 给前端判断:
//   - licensed=true → 不弹窗, 正常进工具箱
//   - licensed=false → 弹激活窗, reason 决定提示语:
//       * no-local-cert: 首次使用, 显示"请输入激活码"
//       * cert-corrupted: 证书损坏, 显示"请重新激活"
//       * ip-mismatch: IP 变了 (换了网络), 显示"本机 IP 与首次激活不一致, 请重新激活"
//       * unknown: 其他, 通用提示
func (s *Server) handleLicenseStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return
	}
	st := license.GetStatus()
	writeJSON(w, 200, map[string]any{
		"licensed": st.Licensed,
		"reason":   st.Reason,
	})
}

// handleLicenseActivate POST /api/license/activate
//
// 入参: {code: "激活码"}
// 出参: {ok: bool, error?: string}
//
// 流程: 前端 → Go 端 → Java 服务端 → Go 端写本地证书 → 前端 reload
func (s *Server) handleLicenseActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024)).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求格式错误"))
		return
	}

	if err := license.Activate(req.Code); err != nil {
		// 不论是网络错还是服务端拒绝, 都返回 200 + {ok:false, error:...}
		// 让前端统一处理, 不要用 HTTP 状态码区分业务错误
		writeJSON(w, 200, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, 200, map[string]any{"ok": true})
}