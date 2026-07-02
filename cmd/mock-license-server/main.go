// mock-license-server 是一个独立的 Go 程序, 用来模拟 Java 端的 /kairo/auth/activate 行为。
//
// 用途:
//   - 本地开发时替代真实 Java 后端 + Oracle, 跑集成测试
//   - 没有 Oracle 数据库也能跑 (用内存 map 存)
//   - 启动:  go run ./cmd/mock-license-server -addr :18091 -auth TEST_TOKEN_123
//   - 预置激活码: test-code-001 / test-code-002 / demo-001 (IP 字段为空, 首次激活会写入)
//   - 调试查询:
//       curl http://localhost:18091/admin/list     # 所有激活码状态
//       curl http://localhost:18091/admin/audit    # 所有审计日志 (防内鬼查证)
//       curl -X POST http://localhost:18091/admin/reset  # 重置所有绑定
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// activationRecord 模拟 K_ACT_CODE 表的一行
type activationRecord struct {
	Code   string `json:"code"`
	IP     string `json:"ip"`
	UsedAt string `json:"used_at,omitempty"`
}

// auditRecord 模拟 K_AUDIT 表的一行
type auditRecord struct {
	ID        int64  `json:"id"`
	EVT       string `json:"evt"`
	Code      string `json:"code,omitempty"`
	IP        string `json:"ip,omitempty"`
	Info      string `json:"info,omitempty"`
	CreatedAt string `json:"created_at"`
}

var (
	mu       sync.Mutex
	bindings = map[string]*activationRecord{}
	audits   []auditRecord
	auditSeq int64

	adminAuthToken string // 由 main() 设置, /admin/* 和 /activate 共用
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18091", "监听地址 (默认仅本机, 避免误暴露)")
	authToken := flag.String("auth", "TEST_TOKEN_123", "Basic auth 校验串 (放在 Authorization: Basic 后面)")
	presetCodes := flag.String("preset", "test-code-001,test-code-002,demo-001", "预置激活码, 逗号分隔")
	flag.Parse()

	adminAuthToken = *authToken

	for _, c := range strings.Split(*presetCodes, ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			bindings[c] = &activationRecord{Code: c}
		}
	}
	log.Printf("mock-license-server 启动: addr=%s auth=%s 预置 %d 个激活码", *addr, *authToken, len(bindings))
	for c := range bindings {
		log.Printf("  - %s", c)
	}

	mux := http.NewServeMux()
	// 兼容两套接口: 旧 /kairo/auth/activate (Java 直连风格) + 新 /credit/httpInterface (通用 credit 网关风格)
	// 业务逻辑完全相同 (激活码↔IP 绑定 + 审计), handler 复用。
	mux.HandleFunc("/kairo/auth/activate", handleActivate)
	mux.HandleFunc("/credit/httpInterface", handleActivate)
	mux.HandleFunc("/admin/list", requireAdminAuth(handleAdminList))
	mux.HandleFunc("/admin/audit", requireAdminAuth(handleAdminAudit))
	mux.HandleFunc("/admin/reset", requireAdminAuth(handleAdminReset))
	mux.HandleFunc("/admin/suspicious", requireAdminAuth(handleAdminSuspicious))
	mux.HandleFunc("/health", handleHealth)

	log.Printf("可用端点:")
	log.Printf("  POST /kairo/auth/activate    激活接口 (旧, Java 直连风格)")
	log.Printf("  POST /credit/httpInterface   激活接口 (新, 通用 credit 网关风格)")
	log.Printf("  GET  /admin/list             查看所有激活码状态 (需 Basic auth)")
	log.Printf("  GET  /admin/audit            查看所有审计日志 (需 Basic auth)")
	log.Printf("  GET  /admin/suspicious       查可疑激活码 (需 Basic auth)")
	log.Printf("  POST /admin/reset            重置所有绑定 (需 Basic auth)")
	log.Printf("  GET  /health                 健康检查")
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// writeAudit 写入审计日志 (线程安全)
func writeAudit(code, ip, evt, info string) {
	rec := auditRecord{
		ID:        atomic.AddInt64(&auditSeq, 1),
		EVT:       evt,
		Code:      code,
		IP:        ip,
		Info:      info,
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	audits = append(audits, rec)
}

// handleActivate 模拟 Java 端的 /kairo/auth/activate
func handleActivate(w http.ResponseWriter, r *http.Request) {
	// 1. Basic auth 校验
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Basic ") {
		writeAudit("", "", "REJECT_BAD_AUTH", "missing Basic auth header")
		writeJSON(w, 401, map[string]any{"ok": false, "error": "missing Basic auth"})
		return
	}
	token := strings.TrimPrefix(auth, "Basic ")
	if !mockAuthPass(token) {
		writeAudit("", "", "REJECT_BAD_AUTH", "bad auth token: "+maskToken(token))
		writeJSON(w, 401, map[string]any{"ok": false, "error": "bad auth token"})
		return
	}

	// 2. 读 body
	body, _ := io.ReadAll(r.Body)
	var req map[string]string
	if err := json.Unmarshal(body, &req); err != nil {
		writeAudit("", "", "REJECT_BAD_PARAMS", "bad json: "+err.Error())
		writeJSON(w, 400, map[string]any{"ok": false, "error": "bad json: " + err.Error()})
		return
	}
	code := req["secret_key"]
	ip := req["ip"]

	if code == "" || ip == "" {
		writeAudit(code, ip, "REJECT_BAD_PARAMS", "missing secret_key or ip")
		writeJSON(w, 200, map[string]any{"ok": false, "error": "参数缺失 (secret_key / ip)"})
		return
	}

	log.Printf("[activate] code=%s ip=%s", code, ip)

	// 3. 业务逻辑
	mu.Lock()
	defer mu.Unlock()

	rec, exists := bindings[code]
	if !exists {
		writeAudit(code, ip, "REJECT_CODE_INVALID", "code not found")
		log.Printf("[activate] reject: 激活码 %s 不存在", code)
		writeJSON(w, 200, map[string]any{"ok": false, "error": "激活码无效"})
		return
	}

	// 4. 首次激活 - 模拟 SQL UPDATE 影响行数检查 (防止并发竞态)
	// 真实 Java 端应该用 SELECT FOR UPDATE 锁行 / 检查 UPDATE ROWCOUNT
	// mock 这里简化: 在锁保护下检查"我即将设置的 IP 是否跟当前空 IP 冲突"
	if rec.IP == "" {
		// 模拟: 在锁里, 但另一个并发请求理论上可以插进来
		// 真实场景: 如果两个并发请求都看到 IP="" 并都尝试 UPDATE,
		// 一个 UPDATE 影响 1 行, 另一个影响 0 行
		// 这里通过直接检查 rec.IP 仍然为空来模拟"成功"
		rec.IP = ip
		rec.UsedAt = time.Now().Format("2006-01-02 15:04:05")
		writeAudit(code, ip, "ACTIVATE_OK", "first activate")
		log.Printf("[activate] 首次激活成功: code=%s → ip=%s", code, ip)
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}

	// 5. IP 不匹配 → 拒绝 + 写审计 (防内鬼关键点)
	if rec.IP != ip {
		info := fmt.Sprintf("db_ip=%s req_ip=%s (可能内鬼给了别人)", rec.IP, ip)
		writeAudit(code, ip, "REJECT_IP_MISMATCH", info)
		log.Printf("[activate] reject IP 不匹配: code=%s db_ip=%s req_ip=%s", code, rec.IP, ip)
		writeJSON(w, 200, map[string]any{"ok": false, "error": "激活码已被其他机器使用"})
		return
	}

	// 6. 重激活
	rec.UsedAt = time.Now().Format("2006-01-02 15:04:05")
	writeAudit(code, ip, "REACTIVATE_OK", "reactivate")
	log.Printf("[activate] 重激活: code=%s ip=%s", code, ip)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleAdminList 查看所有激活码状态
func handleAdminList(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	out := make([]*activationRecord, 0, len(bindings))
	for _, rec := range bindings {
		out = append(out, rec)
	}
	writeJSON(w, 200, out)
}

// handleAdminAudit 查看所有审计日志 (防内鬼查证)
func handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	// 支持按 evt 过滤: ?evt=REJECT_IP_MISMATCH
	evtFilter := r.URL.Query().Get("evt")
	if evtFilter == "" {
		writeJSON(w, 200, audits)
		return
	}
	filtered := make([]auditRecord, 0)
	for _, a := range audits {
		if a.EVT == evtFilter {
			filtered = append(filtered, a)
		}
	}
	writeJSON(w, 200, filtered)
}

// handleAdminSuspicious 查可疑激活码 (多个 IP 尝试过) - 防内鬼快捷查询
func handleAdminSuspicious(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	// 按 code 分组, 看有几个不同 IP 出现过
	codeIPs := map[string]map[string]bool{}
	for _, a := range audits {
		if a.Code == "" {
			continue
		}
		if _, ok := codeIPs[a.Code]; !ok {
			codeIPs[a.Code] = map[string]bool{}
		}
		if a.IP != "" {
			codeIPs[a.Code][a.IP] = true
		}
	}

	type suspiciousCode struct {
		Code       string   `json:"code"`
		IPCount    int      `json:"ip_count"`
		IPs        []string `json:"ips"`
		LastEvent  string   `json:"last_event"`
		LastTime   string   `json:"last_time"`
	}

	var suspicious []suspiciousCode
	for code, ips := range codeIPs {
		if len(ips) > 1 {
			ipList := make([]string, 0, len(ips))
			for ip := range ips {
				ipList = append(ipList, ip)
			}
			// 找最近一条
			var lastEvt, lastTime string
			for i := len(audits) - 1; i >= 0; i-- {
				if audits[i].Code == code {
					lastEvt = audits[i].EVT
					lastTime = audits[i].CreatedAt
					break
				}
			}
			suspicious = append(suspicious, suspiciousCode{
				Code:      code,
				IPCount:   len(ips),
				IPs:       ipList,
				LastEvent: lastEvt,
				LastTime:  lastTime,
			})
		}
	}

	writeJSON(w, 200, suspicious)
}

// handleAdminReset 重置所有绑定 (IP 清空, 清空审计)
func handleAdminReset(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	for _, rec := range bindings {
		rec.IP = ""
		rec.UsedAt = ""
	}
	audits = nil
	atomic.StoreInt64(&auditSeq, 0)
	writeJSON(w, 200, map[string]any{"ok": true, "reset_count": len(bindings), "audit_cleared": true})
}

// handleHealth 健康检查
func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "service": "mock-license-server"})
}

// requireAdminAuth 包装 /admin/* handler, 要求 Basic auth 与 -auth 配置的 token 一致。
func requireAdminAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			w.Header().Set("WWW-Authenticate", `Basic realm="mock-admin"`)
			writeJSON(w, 401, map[string]any{"error": "admin requires auth"})
			return
		}
		token := strings.TrimPrefix(auth, "Basic ")
		if !mockAuthPass(token) {
			w.Header().Set("WWW-Authenticate", `Basic realm="mock-admin"`)
			writeJSON(w, 401, map[string]any{"error": "invalid auth token"})
			return
		}
		handler(w, r)
	}
}

// mockAuthPass 校验 token 是否与 -auth 配置的一致。
func mockAuthPass(token string) bool {
	if token == "" || token == "PLACEHOLDER_BASIC_AUTH" {
		return false
	}
	return token == adminAuthToken
}

func maskToken(t string) string {
	if t == "" || len(t) < 4 {
		return "***"
	}
	return t[:2] + "***" + t[len(t)-2:]
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}