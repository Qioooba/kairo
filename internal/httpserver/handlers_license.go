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
	if s.skipLicenseCheck {
		writeJSON(w, 200, map[string]any{"licensed": true, "reason": "test"})
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
//
// 审计: 每次激活都写 audit (成功/失败/网络错), 防内鬼核心数据源 (跟 Java 端 K_AUDIT 对齐)
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

	// 记请求本身 (激活码做脱敏, 只保留前4后2)
	maskedCode := maskCode(req.Code)
	s.audit.Write("license.activate.request", "code", maskedCode)

	if err := license.Activate(req.Code); err != nil {
		// 失败: 详细错误写审计日志, 前端只显示通用提示 (避免泄露内网地址)
		s.audit.Write("license.activate.fail", "code", maskedCode, "err", err.Error())
		writeJSON(w, 200, map[string]any{
			"ok":    false,
			"error": "激活失败, 请检查激活码是否正确或稍后重试",
		})
		return
	}

	s.audit.Write("license.activate.ok", "code", maskedCode)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// maskCode 脱敏激活码, 保留前4后2, 中间用 **** 代替。
// 短于 6 位的激活码全部用 **** 代替。
func maskCode(code string) string {
	if len(code) <= 6 {
		return "****"
	}
	return code[:4] + "****" + code[len(code)-2:]
}