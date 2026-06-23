package httpserver

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

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

// redactRecord 把 Record 里敏感字段值替换为 "<redacted>"，
// 返回新的 map（不修改原 Record 的 KV）。
//
// 与 audit.WriteJSON 共用 isSensitiveKey 规则：B2 安全要求 ——
// password / private_key / host_key_sha256 / api_key 等任何导出渠道
// 都必须脱敏，不能因为改了导出格式又泄露。
func redactRecord(rec audit.Record) (cleanedKV map[string]string, redactedKeys []string) {
	cleanedKV = make(map[string]string, len(rec.KV))
	for k, v := range rec.KV {
		if audit.IsSensitiveKey(k) {
			redactedKeys = append(redactedKeys, k)
			cleanedKV[k] = audit.JSONRedactedValue
			continue
		}
		cleanedKV[k] = v
	}
	return cleanedKV, redactedKeys
}

// handleAuditExportJSON 把操作历史导出成 JSON 文件（B2 新功能）。
//
// 路由：GET /api/audit/export.json
// 参数：跟 /api/audit/recent 一致（op/system/server/result/limit）
// 响应：application/json 附件，文件名 ops-toolbox-audit-YYYYMMDD-HHMMSS.json
//
// 安全要点：
//   - 敏感字段（password / private_key / host_key_sha256 / api_key 等）
//     统一替换为 "<redacted>"，并在行内 redacted 数组里列出被脱敏的 key 名；
//   - 同样适用 limit 上限（5000 行），避免大文件爆内存。
func (s *Server) handleAuditExportJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 5000
	}
	if limit > 5000 {
		limit = 5000
	}
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
	// 在内存里做一次脱敏；audit.WriteJSON 会再脱一遍，但中间层
	// 也走一次的好处：万一以后换导出格式（XML / YAML）也能直接复用。
	cleaned := make([]audit.Record, 0, len(recs))
	for _, rec := range recs {
		cleanKV, _ := redactRecord(rec)
		cleaned = append(cleaned, audit.Record{Time: rec.Time, Op: rec.Op, KV: cleanKV, Raw: rec.Raw})
	}

	filename := audit.JSONFilename(time.Now())
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="ops-toolbox-`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := audit.WriteJSON(w, cleaned); err != nil {
		// 已经写一部分了；不能改 status。仅记 server 日志。
		// 用 stderr 兜底（前端可能拿到的是截断 JSON，浏览器会显示"解析失败"）。
		fmt.Fprintf(w, "\n/* encode error: %v */\n", err)
	}
}

// handleAuditExportCSV 把操作历史导出成 CSV 文件（项 24 P3）。
//
// 路由：GET /api/audit/export.csv
// 参数：跟 /api/audit/recent 一样（op/system/server/result/limit）
// 响应：text/csv 附件，文件名 ops-toolbox-audit-YYYYMMDD-HHMMSS.csv
//
// 设计要点：
//   - 字段顺序固定，方便 Excel / 脚本后续处理；
//   - 全部字段都加双引号 + 标准 CSV 转义（处理含逗号 / 引号 / 换行的值）；
//   - 限制最大行数（5000，跟 Recent 上限一致），避免大文件爆内存。
func (s *Server) handleAuditExportCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 5000
	}
	if limit > 5000 {
		limit = 5000
	}
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

	// 通用字段列
	header := []string{"ts", "op", "system", "server", "result", "dir", "file", "query", "stage", "bytes", "hits", "id", "lines", "files", "count", "err", "raw"}
	// 收集 KV 里"不在 header 里"的其它字段（保证数据完整性）
	extraKeys := map[string]bool{}
	for _, rec := range recs {
		cleanKV, _ := redactRecord(rec)
		for k := range cleanKV {
			if !containsStr(header, k) && k != "" && !audit.IsSensitiveKey(k) {
				extraKeys[k] = true
			}
		}
	}
	extras := make([]string, 0, len(extraKeys))
	for k := range extraKeys {
		extras = append(extras, k)
	}
	allCols := append(header, extras...)

	filename := fmt.Sprintf("ops-toolbox-audit-%s.csv", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	cw := csv.NewWriter(w)
	// BOM 头让 Excel 默认按 UTF-8 打开（避免中文乱码）
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	if err := cw.Write(allCols); err != nil {
		// 已经写出去一点了，不能改 status；只能记到 server 日志
		return
	}
	for _, rec := range recs {
		cleanKV, _ := redactRecord(rec)
		row := make([]string, 0, len(allCols))
		// 标准列
		row = append(row,
			rec.Time.Format("2006-01-02 15:04:05.000"),
			rec.Op,
			cleanKV["system"],
			cleanKV["server"],
			cleanKV["result"],
			cleanKV["dir"],
			cleanKV["file"],
			cleanKV["query"],
			cleanKV["stage"],
			cleanKV["bytes"],
			cleanKV["hits"],
			cleanKV["id"],
			cleanKV["lines"],
			cleanKV["files"],
			cleanKV["count"],
			cleanKV["err"],
			rec.Raw,
		)
		// 其它 extras（按出现顺序补齐，确保列对齐）
		for _, k := range extras {
			row = append(row, cleanKV[k])
		}
		if err := cw.Write(row); err != nil {
			return
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		// 同样已经 flush 了一部分
		_ = err
	}
}

// containsStr 简单字符串包含（避免引入 slices.Contains 兼容性麻烦）。
func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
