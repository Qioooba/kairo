// sync 客户端单元测试
//
// 覆盖:
//  1. URL 拼装: channelID=PC, serviceID=KairoPetLeaderboardAction, seqNo=<秒级时间戳>
//  2. InitFromConfig 覆盖 var 池 (空值不覆盖)
//  3. 端点未配置 → SyncNow 直接返 "pet leaderboard endpoint 未配置"
//  4. 请求体形状: user_id/name/level/stage/exp_total/board_exp/ledger/ts 全带上
//  5. ok=true 时 MarkSynced 生效 (BoardExp 回写 + dirty 清除)
//  6. ok=false 时 MarkSynced 不生效 (旧分保留 + dirty 保留)
//  7. Basic Auth header 带上
//
// 注意: 测试里改 var 池 (syncPrimary 等) 用 restorePetSyncVars 还原, 避免污染其他测试。
package pet

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kairo/internal/audit"
)

// restorePetSyncVars 在测试结束时还原 sync var 池, 避免污染其他测试
func restorePetSyncVars(t *testing.T) {
	t.Helper()
	prim, sec, auth := syncPrimary, syncSecondary, syncAuth
	to := syncTimeout
	t.Cleanup(func() {
		syncPrimary = prim
		syncSecondary = sec
		syncAuth = auth
		syncTimeout = to
	})
}

// newSyncTestEngine 构造一个临时目录 + 默认规则的引擎 (未解锁, 测试里按需 Enable)。
// 命名带 sync 前缀, 避免跟 engine_test.go 的 newTestEngine 撞名。
func newSyncTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(DefaultRules(), filepath.Join(t.TempDir(), "pet.json"), nil, nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

// TestBuildSyncURL 验证 URL 拼装 (channelID / serviceID / seqNo)
func TestBuildSyncURL(t *testing.T) {
	u := buildSyncURL("http://66.0.34.199:9080/credit/httpInterface")
	if !strings.Contains(u, "channelID=PC") {
		t.Errorf("URL 应含 channelID=PC, got: %s", u)
	}
	if !strings.Contains(u, "serviceID=KairoPetLeaderboardAction") {
		t.Errorf("URL 应含 serviceID=KairoPetLeaderboardAction, got: %s", u)
	}
	if !strings.Contains(u, "seqNo=") {
		t.Errorf("URL 应含 seqNo=, got: %s", u)
	}
	// 已带 query 的 base 用 & 追加
	u2 := buildSyncURL("http://x/y?a=1")
	if !strings.Contains(u2, "&channelID=PC") {
		t.Errorf("已带 query 的 URL 应用 & 追加, got: %s", u2)
	}
}

// TestInitFromConfig 验证 var 池覆盖 (空值不动 var, 跟 sponsor 同款)
func TestInitFromConfig(t *testing.T) {
	restorePetSyncVars(t)

	InitFromConfig("http://primary:1234/x", "http://secondary:5678/y", "dGVzdA==", 7*time.Second)
	if syncPrimary != "http://primary:1234/x" {
		t.Errorf("syncPrimary 未被覆盖: got=%q", syncPrimary)
	}
	if syncSecondary != "http://secondary:5678/y" {
		t.Errorf("syncSecondary 未被覆盖: got=%q", syncSecondary)
	}
	if syncAuth != "dGVzdA==" {
		t.Errorf("syncAuth 未被覆盖: got=%q", syncAuth)
	}
	if syncTimeout != 7*time.Second {
		t.Errorf("syncTimeout 未被覆盖: got=%v", syncTimeout)
	}

	// 留空字段不动 var
	InitFromConfig("", "", "", 0)
	if syncPrimary != "http://primary:1234/x" {
		t.Errorf("空 Primary 不应清空, got=%q", syncPrimary)
	}
	if syncSecondary != "http://secondary:5678/y" {
		t.Errorf("空 Secondary 不应清空, got=%q", syncSecondary)
	}
	if syncAuth != "dGVzdA==" {
		t.Errorf("空 auth 不应清空, got=%q", syncAuth)
	}
	if syncTimeout != 7*time.Second {
		t.Errorf("空 timeout 不应清空, got=%v", syncTimeout)
	}
}

// TestSyncNow_UnsetPrimary 验证端点未配置时直接报错, 不打网络
func TestSyncNow_UnsetPrimary(t *testing.T) {
	restorePetSyncVars(t)
	syncPrimary = ""
	syncSecondary = ""

	e := newSyncTestEngine(t)
	if _, err := e.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	res, err := e.SyncNow()
	if err == nil {
		t.Fatal("端点未配置应返 err")
	}
	if res != nil {
		t.Errorf("端点未配置应返 nil result, got=%+v", res)
	}
	if !strings.Contains(err.Error(), "未配置") {
		t.Errorf("err 应提到'未配置', got: %v", err)
	}
}

// TestSyncNow_RequestShape 验证请求体字段形状 + ok=true 时 MarkSynced 生效
func TestSyncNow_RequestShape(t *testing.T) {
	restorePetSyncVars(t)

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("应 POST, got=%s", r.Method)
		}
		ct := r.Header.Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type 应含 application/json, got=%q", ct)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("请求体不是合法 JSON: %v (body=%s)", err, raw)
		}
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":          true,
			"board_exp":   42,
			"rank":        3,
			"server_time": "2026-08-16T12:00:00+08:00",
			"leaderboard": []map[string]any{
				{"rank": 1, "name": "豆豆", "level": 9, "exp": 100},
				{"rank": 2, "name": "毛毛", "level": 7, "exp": 80},
			},
		})
	}))
	defer srv.Close()

	syncPrimary = srv.URL + "/credit/httpInterface"
	syncSecondary = ""
	syncAuth = "test-auth"

	e := newSyncTestEngine(t)
	if _, err := e.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	// State() 返回深拷贝, 直接改没有用 —— 走 OnOp 白名单 op 产生真实流水 + dirty
	// (files.download 在默认 ExpRules 表内, v2 中价值档 2 分/次, 会 append ledger 并置 Dirty)
	e.OnOp(audit.OpEvent{Op: "files.download"})
	if !e.State().Dirty {
		t.Fatal("OnOp 后 Dirty 应为 true")
	}

	res, err := e.SyncNow()
	if err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if !res.OK {
		t.Fatalf("res.OK 应为 true, got=false, err=%s", res.Error)
	}
	if res.BoardExp != 42 {
		t.Errorf("BoardExp 应为 42, got=%d", res.BoardExp)
	}
	if res.Rank != 3 {
		t.Errorf("Rank 应为 3, got=%d", res.Rank)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("leaderboard 应有 2 项, got=%d", len(res.Entries))
	}
	if res.Entries[0].Name != "豆豆" {
		t.Errorf("第 1 名应叫豆豆, got=%q", res.Entries[0].Name)
	}
	if res.Entries[0].Exp != 100 {
		t.Errorf("第 1 名 exp 应为 100, got=%d", res.Entries[0].Exp)
	}

	// 请求体字段形状
	if gotBody["user_id"] == "" {
		t.Errorf("user_id 不能为空: %v", gotBody)
	}
	if gotBody["name"] == "" {
		t.Errorf("name 不能为空: %v", gotBody)
	}
	if gotBody["level"] == nil {
		t.Errorf("level 缺失: %v", gotBody)
	}
	if gotBody["stage"] == nil {
		t.Errorf("stage 缺失: %v", gotBody)
	}
	if gotBody["exp_total"] == nil || gotBody["board_exp"] == nil {
		t.Errorf("exp_total/board_exp 缺失: %v", gotBody)
	}
	ledger, ok := gotBody["ledger"].([]any)
	if !ok || len(ledger) != 1 {
		t.Fatalf("ledger 应有 1 条, got=%v", gotBody["ledger"])
	}
	entry := ledger[0].(map[string]any)
	if entry["op"] != "files.download" {
		t.Errorf("ledger[0].op 应为 files.download, got=%v", entry["op"])
	}
	if entry["exp"] != float64(2) {
		t.Errorf("ledger[0].exp 应为 2, got=%v", entry["exp"])
	}
	if gotBody["ts"] == "" {
		t.Errorf("ts 缺失: %v", gotBody)
	}

	// ok=true → MarkSynced 生效: BoardExp 回写, dirty 清除
	after := e.State()
	if after.BoardExp != 42 {
		t.Errorf("MarkSynced 后 BoardExp 应为 42, got=%d", after.BoardExp)
	}
	if after.Dirty {
		t.Errorf("MarkSynced 后 Dirty 应为 false")
	}
}

// TestSyncNow_OKFalse_NoMarkSynced 验证服务器业务拒绝时旧分保留 + dirty 保留
func TestSyncNow_OKFalse_NoMarkSynced(t *testing.T) {
	restorePetSyncVars(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "K_PET 表锁住, 请稍后重试",
		})
	}))
	defer srv.Close()

	syncPrimary = srv.URL + "/credit/httpInterface"
	syncSecondary = ""

	e := newSyncTestEngine(t)
	if _, err := e.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	// 走 OnOp 产生真实 dirty (State() 是深拷贝, 直改无效)
	e.OnOp(audit.OpEvent{Op: "files.download"})
	if !e.State().Dirty {
		t.Fatal("OnOp 后 Dirty 应为 true")
	}

	res, err := e.SyncNow()
	if err != nil {
		t.Fatalf("Java 业务失败不应返 Go err, got: %v", err)
	}
	if res.OK {
		t.Error("res.OK 应为 false")
	}
	if res.Error != "K_PET 表锁住, 请稍后重试" {
		t.Errorf("Error 不对: %q", res.Error)
	}

	// ok=false → 不调 MarkSynced: BoardExp 保持 0, dirty 保持 true
	after := e.State()
	if after.BoardExp != 0 {
		t.Errorf("业务拒绝后 BoardExp 应保持 0, got=%d", after.BoardExp)
	}
	if !after.Dirty {
		t.Errorf("业务拒绝后 Dirty 应保持 true (下次同步重试)")
	}
}

// TestSyncNow_BasicAuth 验证 Authorization: Basic <auth> header 带上
func TestSyncNow_BasicAuth(t *testing.T) {
	restorePetSyncVars(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "board_exp": 0, "rank": 0, "leaderboard": []any{}})
	}))
	defer srv.Close()

	syncPrimary = srv.URL + "/credit/httpInterface"
	syncSecondary = ""
	syncAuth = "dGVzdDp0ZXN0"

	e := newSyncTestEngine(t)
	if _, err := e.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if _, err := e.SyncNow(); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if gotAuth != "Basic dGVzdDp0ZXN0" {
		t.Errorf("Authorization header 不对, got=%q want=%q", gotAuth, "Basic dGVzdDp0ZXN0")
	}
}
