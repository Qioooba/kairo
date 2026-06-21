package httpserver

import (
	"errors"
	"net/http"
	"strconv"

	"ops-toolbox/internal/audit"
)

//   - op=xxx   按 op= 精确过滤（如 ssh.test / logs.list / logs.search / logs.download / logs.tail）
//   - system=xxx  按 system 包含过滤
//   - server=xxx  按 server 包含过滤
//   - result=ok|fail  按 result 过滤
//
// 返回 {records: [{ts, op, system, server, raw}], path: "..."}
func (s *Server) handleAuditRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := audit.Filter{
		Op:     q.Get("op"),
		System: q.Get("system"),
		Server: q.Get("server"),
		Result: q.Get("result"),
	}
	recs, err := s.audit.Recent(limit, f)
	if err != nil {
		writeErrSanitized(w, 500, err)
		return
	}
	out := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		row := map[string]any{
			"ts":     rec.Time.Format("2006-01-02 15:04:05.000"),
			"op":     rec.Op,
			"system": rec.KV["system"],
			"server": rec.KV["server"],
			"result": rec.KV["result"],
			"raw":    rec.Raw,
		}
		// 把其它常用字段也单独提出来
		for _, k := range []string{"dir", "file", "query", "stage", "bytes", "hits", "id", "lines", "files", "err", "count"} {
			if v, ok := rec.KV[k]; ok && v != "" {
				row[k] = v
			}
		}
		out = append(out, row)
	}
	writeJSON(w, 200, map[string]any{
		"records": out,
		"count":   len(out),
	})
}
