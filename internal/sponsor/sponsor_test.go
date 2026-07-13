// sponsor 客户端单元测试
//
// 覆盖:
//   1. URL 拼装: channelID=PC, serviceID=KairoSponsorLeaderboardAction, seqNo=<秒级时间戳>
//   2. InitFromConfig 覆盖 var 池
//   3. FetchLeaderboard 成功路径 (200 + {ok:true, entries:[...]})
//   4. FetchLeaderboard Java 端返 {ok:false, error:"..."} 时返回带 OK=false 的结构
//   5. FetchLeaderboard 网络错 → 返 (nil, err)
//   6. FetchLeaderboard 主备切换
//   7. FetchLeaderboard JSON 解析失败 → 返 (nil, err)
//   8. FetchLeaderboard 带 Basic Auth header
//   9. FetchLeaderboard 空 Entries (排行榜没人) → 返 OK=true + 空数组
package sponsor

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// restoreSponsorVars 在测试结束时还原 var 池, 避免污染其他测试
func restoreSponsorVars(t *testing.T) {
	t.Helper()
	prim, sec, auth := Primary, Secondary, BasicAuthHeader
	to := DefaultTimeout
	t.Cleanup(func() {
		Primary = prim
		Secondary = sec
		BasicAuthHeader = auth
		DefaultTimeout = to
	})
}

// TestBuildURL 验证 URL 拼装 + 时间戳 sentinel
func TestBuildURL(t *testing.T) {
	// 取 2 次, 验证每次 seqNo 都不同 (1 秒间隔)
	url1 := buildURL("http://66.0.34.199:9080/credit/httpInterface")
	time.Sleep(1100 * time.Millisecond)
	url2 := buildURL("http://66.0.34.199:9080/credit/httpInterface")

	// 验证 serviceID / channelID 都在
	for _, u := range []string{url1, url2} {
		if !strings.Contains(u, "channelID=PC") {
			t.Errorf("URL 应含 channelID=PC, got: %s", u)
		}
		if !strings.Contains(u, "serviceID="+ServiceID) {
			t.Errorf("URL 应含 serviceID=%s, got: %s", ServiceID, u)
		}
		if !strings.Contains(u, "seqNo=") {
			t.Errorf("URL 应含 seqNo=, got: %s", u)
		}
	}

	// 验证两次 seqNo 不同 (跨秒)
	extractSeq := func(u string) string {
		idx := strings.Index(u, "seqNo=")
		if idx < 0 {
			return ""
		}
		return u[idx+len("seqNo="):]
	}
	if extractSeq(url1) == extractSeq(url2) {
		t.Errorf("seqNo 跨秒应不同: %s vs %s", extractSeq(url1), extractSeq(url2))
	}
}

// TestInitFromConfig 验证 var 池覆盖
func TestInitFromConfig(t *testing.T) {
	restoreSponsorVars(t)

	InitFromConfig("http://primary:1234/x", "http://secondary:5678/y", "dGVzdA==", 7*time.Second)

	if Primary != "http://primary:1234/x" {
		t.Errorf("Primary 未被覆盖: got=%q", Primary)
	}
	if Secondary != "http://secondary:5678/y" {
		t.Errorf("Secondary 未被覆盖: got=%q", Secondary)
	}
	if BasicAuthHeader != "dGVzdA==" {
		t.Errorf("BasicAuthHeader 未被覆盖: got=%q", BasicAuthHeader)
	}
	if DefaultTimeout != 7*time.Second {
		t.Errorf("DefaultTimeout 未被覆盖: got=%v", DefaultTimeout)
	}

	// 留空字段不动 var (重要: 测试代码可能只想覆盖部分字段)
	InitFromConfig("", "", "", 0)
	if Primary != "http://primary:1234/x" {
		t.Errorf("空 Primary 不应清空, got=%q", Primary)
	}
}

// TestFetchLeaderboard_Success 验证成功响应解析
func TestFetchLeaderboard_Success(t *testing.T) {
	restoreSponsorVars(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type 应含 application/json, got=%q", ct)
		}
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"entries": []map[string]any{
				{"rank": 1, "real_name": "张三", "cotti": 4, "lucky": 4, "milktea": 1, "total": 9, "date": "07-09", "updated_at": "2026-07-13 11:40:30.0"},
				{"rank": 2, "real_name": "李四", "cotti": 3, "lucky": 4, "milktea": 1, "total": 8, "date": "07-08", "updated_at": "2026-07-13 11:40:30.0"},
				{"rank": 3, "real_name": "王五", "cotti": 0, "lucky": 5, "milktea": 0, "total": 5, "date": "07-07", "updated_at": "2026-07-13 11:40:30.0"},
			},
		})
	}))
	defer srv.Close()

	Primary = srv.URL + "/credit/httpInterface"
	Secondary = ""
	BasicAuthHeader = "test-auth"
	DefaultTimeout = 2 * time.Second

	lr, err := FetchLeaderboard()
	if err != nil {
		t.Fatalf("FetchLeaderboard: %v", err)
	}
	if !lr.OK {
		t.Errorf("lr.OK 应为 true, got=false, err=%s", lr.Error)
	}
	if len(lr.Entries) != 3 {
		t.Fatalf("Entries 应有 3 项, got=%d", len(lr.Entries))
	}
	if lr.Entries[0].RealName != "张三" {
		t.Errorf("第 1 名应叫张三, got=%q", lr.Entries[0].RealName)
	}
	if lr.Entries[0].Total != 9 {
		t.Errorf("第 1 名 total 应为 9, got=%d", lr.Entries[0].Total)
	}
	if lr.Entries[2].Milktea != 0 {
		t.Errorf("第 3 名 milktea 应为 0, got=%d", lr.Entries[2].Milktea)
	}
	// v0.16: 验证 updated_at 是真实生产 Java 端格式 (空格分隔 + ".0" 毫秒后缀)
	// 这个 case 之前没有 updated_at 字段, 所以单元测试一直过, 但生产炸 —— 教训
	if lr.Entries[0].UpdatedAt != "2026-07-13 11:40:30.0" {
		t.Errorf("updated_at 应透传为原始 string, got=%q", lr.Entries[0].UpdatedAt)
	}
}

// TestFetchLeaderboard_Empty 验证空排行榜 (没人赞助)
func TestFetchLeaderboard_Empty(t *testing.T) {
	restoreSponsorVars(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"entries": []any{},
		})
	}))
	defer srv.Close()

	Primary = srv.URL + "/credit/httpInterface"
	Secondary = ""

	lr, err := FetchLeaderboard()
	if err != nil {
		t.Fatalf("FetchLeaderboard: %v", err)
	}
	if !lr.OK {
		t.Errorf("lr.OK 应为 true, got=false")
	}
	if len(lr.Entries) != 0 {
		t.Errorf("Entries 应为空, got=%d", len(lr.Entries))
	}
}

// TestFetchLeaderboard_JavaFail 验证 Java 端返 {ok:false, error:"..."} 时
// 应该返带 OK=false 的结构 (不是 Go error, 让 handler 决定怎么响应前端)
func TestFetchLeaderboard_JavaFail(t *testing.T) {
	restoreSponsorVars(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "K_SPONSOR 表锁住, 请稍后重试",
		})
	}))
	defer srv.Close()

	Primary = srv.URL + "/credit/httpInterface"
	Secondary = ""

	lr, err := FetchLeaderboard()
	if err != nil {
		t.Fatalf("Java 业务失败不应返 Go err, got: %v", err)
	}
	if lr.OK {
		t.Errorf("lr.OK 应为 false")
	}
	if lr.Error != "K_SPONSOR 表锁住, 请稍后重试" {
		t.Errorf("Error 不对: %q", lr.Error)
	}
}

// TestFetchLeaderboard_PrimaryFail_UseSecondary 验证主地址挂自动切备
func TestFetchLeaderboard_PrimaryFail_UseSecondary(t *testing.T) {
	restoreSponsorVars(t)

	var secondaryHit int32
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryHit, 1)
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "entries": []any{}})
	}))
	defer secondary.Close()

	Primary = "http://127.0.0.1:1/credit/httpInterface" // 端口 1 没人监听
	Secondary = secondary.URL + "/credit/httpInterface"
	DefaultTimeout = 1 * time.Second

	lr, err := FetchLeaderboard()
	if err != nil {
		t.Fatalf("应自动切到备地址, got err: %v", err)
	}
	if !lr.OK {
		t.Errorf("lr.OK 应为 true, got=false")
	}
	if atomic.LoadInt32(&secondaryHit) != 1 {
		t.Errorf("secondary 应被命中 1 次, got=%d", secondaryHit)
	}
}

// TestFetchLeaderboard_NetworkFail 验证所有地址都挂返 Go err
func TestFetchLeaderboard_NetworkFail(t *testing.T) {
	restoreSponsorVars(t)

	Primary = "http://127.0.0.1:1/credit/httpInterface"
	Secondary = "http://127.0.0.1:2/credit/httpInterface"
	DefaultTimeout = 500 * time.Millisecond

	_, err := FetchLeaderboard()
	if err == nil {
		t.Fatal("所有地址挂应返 err")
	}
}

// TestFetchLeaderboard_BadJSON 验证响应是合法 HTTP 200 但 body 不是 JSON
func TestFetchLeaderboard_BadJSON(t *testing.T) {
	restoreSponsorVars(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		w.Write([]byte("not json {{{"))
	}))
	defer srv.Close()

	Primary = srv.URL + "/credit/httpInterface"
	Secondary = ""

	_, err := FetchLeaderboard()
	if err == nil {
		t.Fatal("JSON 解析失败应返 err")
	}
	if !strings.Contains(err.Error(), "响应解析失败") {
		t.Errorf("err 应提到'响应解析失败', got: %v", err)
	}
}

// TestFetchLeaderboard_BasicAuth 验证 Authorization: Basic <auth> header 带上
func TestFetchLeaderboard_BasicAuth(t *testing.T) {
	restoreSponsorVars(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "entries": []any{}})
	}))
	defer srv.Close()

	Primary = srv.URL + "/credit/httpInterface"
	Secondary = ""
	BasicAuthHeader = "dGVzdDp0ZXN0"

	_, err := FetchLeaderboard()
	if err != nil {
		t.Fatalf("FetchLeaderboard: %v", err)
	}
	if gotAuth != "Basic dGVzdDp0ZXN0" {
		t.Errorf("Authorization header 不对, got=%q want=%q", gotAuth, "Basic dGVzdDp0ZXN0")
	}
}

// TestFetchLeaderboard_BodyIsEmpty 验证请求体是空 JSON 对象
func TestFetchLeaderboard_BodyIsEmpty(t *testing.T) {
	restoreSponsorVars(t)

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "entries": []any{}})
	}))
	defer srv.Close()

	Primary = srv.URL + "/credit/httpInterface"
	Secondary = ""

	_, _ = FetchLeaderboard()
	gotBody = strings.TrimSpace(gotBody)
	if gotBody != "" && gotBody != "{}" {
		t.Errorf("请求体应为空或 {}, got=%q", gotBody)
	}
}

// TestFetchLeaderboard_RealJavaTimeFormat v0.16 加。
//
// 复现 v0.15 之前生产炸的 bug: Java 端 updated_at 返 "2026-07-13 11:40:30.0"
// (Oracle/MySQL DATETIME 文本, 空格分隔 + ".0" 毫秒后缀), 跟 Go encoding/json
// 默认 time.Time 解析器 (RFC3339 "T" 分隔) 不兼容 → "响应解析失败" 审计告警。
//
// 修法: Entry.UpdatedAt 改 string 透传。本测试验证 string 字段能正确收真实格式。
// 数字字段 (rank/cotti/lucky/milktea/total) 在生产 Java 端是 JSON number,
// 这里也用 number 跟生产 1:1, 跟 updated_at string 形成对比。
func TestFetchLeaderboard_RealJavaTimeFormat(t *testing.T) {
	restoreSponsorVars(t)

	// 真实 Java 端会返的 updated_at: 空格分隔 + ".0" 毫秒后缀, 跟 RFC3339 完全不同
	const realUpdatedAt = "2026-07-13 11:40:30.0"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		// 数字字段用 JSON number, updated_at 用真实生产 string 格式
		body := `{
			"ok": true,
			"entries": [
				{"rank": 1, "real_name": "张三", "cotti": 4, "lucky": 4, "milktea": 1, "total": 9, "date": "07-13", "updated_at": "` + realUpdatedAt + `"},
				{"rank": 2, "real_name": "李四", "cotti": 3, "lucky": 4, "milktea": 1, "total": 8, "date": "07-13", "updated_at": "` + realUpdatedAt + `"}
			]
		}`
		w.Write([]byte(body))
	}))
	defer srv.Close()

	Primary = srv.URL + "/credit/httpInterface"
	Secondary = ""
	BasicAuthHeader = "test-auth"

	lr, err := FetchLeaderboard()
	if err != nil {
		t.Fatalf("真实 Java 时间格式应能解析 (string 透传), got err: %v", err)
	}
	if !lr.OK {
		t.Fatalf("lr.OK 应为 true, got=false, err=%s", lr.Error)
	}
	if len(lr.Entries) != 2 {
		t.Fatalf("应有 2 条, got=%d", len(lr.Entries))
	}

	// 关键断言: 真实生产格式能原样收
	if lr.Entries[0].UpdatedAt != realUpdatedAt {
		t.Errorf("updated_at 透传应原样保留, got=%q want=%q", lr.Entries[0].UpdatedAt, realUpdatedAt)
	}
	if lr.Entries[0].Total != 9 {
		t.Errorf("total 应为 9, got=%d", lr.Entries[0].Total)
	}
	if lr.Entries[0].Rank != 1 {
		t.Errorf("rank 应为 1, got=%d", lr.Entries[0].Rank)
	}

	t.Logf("真实 Java 格式解析 OK: updated_at=%q", lr.Entries[0].UpdatedAt)
}
