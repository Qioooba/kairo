package httpserver

import (
	"encoding/json"
	"net/http"

	"kairo/internal/sshclient"
)

// writeJSON 把 v 序列化成 JSON 写到 w，状态码 code。
// 用 encoding/json 包的好处：自动转义、双引号、Unicode 都不用我们管。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr 写一个统一的 {"error": msg} 响应。
//
// 注意：err 字符串原样写到响应。错误信息可能含"password"字面量（来自
// x/crypto/ssh 握手失败的错误信息），所以含用户凭据的 err 必须走
// writeErrSanitized，避免泄到前端 / 浏览器 devtools。
func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// writeErrSanitized 写错误响应但 err 字符串先过 sshclient.SanitizeError。
//
// 适用范围：所有 5xx 响应，特别是 SSH / SFTP / tail 流错路径。
// 因为这些 err 直接来自 x/crypto/ssh 包，可能含明文密码或私钥路径字面量。
func writeErrSanitized(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": sshclient.SanitizeError(err.Error())})
}

// StructuredErrorResponse 提供向后兼容的前端结构化错误响应
type StructuredErrorResponse struct {
	OK           bool   `json:"ok"`
	Error        string `json:"error"`
	Code         string `json:"code,omitempty"`
	OperationID  string `json:"operation_id,omitempty"`
	Retryable    bool   `json:"retryable"`
	EffectStatus string `json:"effect_status,omitempty"` // not_applied | applied | unknown
	Outcome      string `json:"outcome,omitempty"`
}

func writeStructuredErr(w http.ResponseWriter, code int, err error, errCode string, retryable bool, effectStatus string) {
	outcome := effectStatus
	if effectStatus == "unknown" {
		outcome = "outcome_unknown"
	}
	resp := StructuredErrorResponse{
		OK:           false,
		Error:        sshclient.SanitizeError(err.Error()),
		Code:         errCode,
		Retryable:    retryable,
		EffectStatus: effectStatus,
		Outcome:      outcome,
	}
	writeJSON(w, code, resp)
}
