package httpserver

import (
	"encoding/json"
	"net/http"
)

// writeJSON 把 v 序列化成 JSON 写到 w，状态码 code。
// 用 encoding/json 包的好处：自动转义、双引号、Unicode 都不用我们管。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr 写一个统一的 {"error": msg} 响应。
func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}