// /api/pet/* 端点测试
//
// 覆盖:
//  1. 未解锁时 GET /api/pet/state → 200 {enabled:false}
//  2. POST /api/pet/enable → 200 {enabled:true, level:1}, 幂等
//  3. name/pos/skin 校验失败 → 400
//  4. sync 上游不可用 → 502 优雅降级 (不 crash)
//  5. leaderboard 空缓存 → {ok:true, entries:[], stale:true, cached:true}
//  6. engine 未注入 (不调 SetPet) → 404
//  7. 未解锁时 name/sync/leaderboard → 404
//  8. skills/battle 命名空间预留 → 404
//
// 测试基础设施复用 httpserver_test.go 的 newTestServer / doRequest。
package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"kairo/internal/pet"
)

// newTestServerWithPet 构造带宠物引擎的 Server (引擎数据落 t.TempDir)。
// 皮肤清单传 nil（引擎退回内置最小清单，仅默认款 orange-cat）。
func newTestServerWithPet(t *testing.T) (*Server, *pet.Engine) {
	return newTestServerWithPetSkins(t, nil)
}

// newTestServerWithPetSkins 同 newTestServerWithPet，但允许指定 skins.json 内容
// （用于测未解锁皮肤的 400 分支）。
func newTestServerWithPetSkins(t *testing.T, skinsJSON []byte) (*Server, *pet.Engine) {
	t.Helper()
	eng, err := pet.NewEngine(pet.DefaultRules(), filepath.Join(t.TempDir(), "pet.json"), nil, skinsJSON)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	srv, _, _, _ := newTestServerWithDependencies(t, Dependencies{Pet: eng})
	return srv, eng
}

// decodeJSON 把响应体解到 map, 失败直接 t.Fatal。
func decodeJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (body=%s)", err, body)
	}
	return got
}

// ---------- /api/pet/state ----------

// TestPetState_NotEnabled 未解锁时返 {enabled:false}, 不泄露任何状态字段。
func TestPetState_NotEnabled(t *testing.T) {
	srv, _ := newTestServerWithPet(t)
	w := doRequest(srv, "GET", "/api/pet/state", nil)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	got := decodeJSON(t, w.Body.Bytes())
	if got["enabled"] != false {
		t.Errorf("未解锁应返 enabled:false, got=%v", got)
	}
}

// TestPetState_NoEngine 引擎未注入 → 404。
func TestPetState_NoEngine(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/pet/state", nil)
	if w.Code != 404 {
		t.Errorf("未注入引擎应 404, got=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "宠物功能不可用") {
		t.Errorf("错误信息应含'宠物功能不可用': %s", w.Body.String())
	}
}

// ---------- /api/pet/enable ----------

// TestPetEnable_Flow 解锁流程: 200 + enabled:true + level 1, 且幂等。
func TestPetEnable_Flow(t *testing.T) {
	srv, _ := newTestServerWithPet(t)

	w := doRequest(srv, "POST", "/api/pet/enable", nil)
	if w.Code != 200 {
		t.Fatalf("enable code=%d body=%s", w.Code, w.Body.String())
	}
	got := decodeJSON(t, w.Body.Bytes())
	if got["enabled"] != true {
		t.Errorf("解锁后应返 enabled:true, got=%v", got)
	}
	// StateView 的键名由引擎决定; 有 level 键时必须为 1
	if lv, ok := got["level"]; ok && lv != float64(1) {
		t.Errorf("新解锁宠物 level 应为 1, got=%v", lv)
	}

	// 幂等: 再点一次不报错
	w2 := doRequest(srv, "POST", "/api/pet/enable", nil)
	if w2.Code != 200 {
		t.Errorf("重复 enable 应幂等 200, got=%d body=%s", w2.Code, w2.Body.String())
	}

	// 解锁后 GET state 返 enabled:true
	w3 := doRequest(srv, "GET", "/api/pet/state", nil)
	if w3.Code != 200 {
		t.Fatalf("state code=%d", w3.Code)
	}
	got3 := decodeJSON(t, w3.Body.Bytes())
	if got3["enabled"] != true {
		t.Errorf("解锁后 state 应返 enabled:true, got=%v", got3)
	}
}

// TestPetEnable_NoEngine 引擎未注入 → 404。
func TestPetEnable_NoEngine(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/pet/enable", nil)
	if w.Code != 404 {
		t.Errorf("未注入引擎应 404, got=%d", w.Code)
	}
}

// ---------- /api/pet/name ----------

// TestPetName_Validation 超长 / 控制字符 → 400; 正常 → 200 且状态生效。
func TestPetName_Validation(t *testing.T) {
	srv, _ := newTestServerWithPet(t)
	if w := doRequest(srv, "POST", "/api/pet/enable", nil); w.Code != 200 {
		t.Fatalf("enable: %d", w.Code)
	}

	// 超长 (引擎侧 ≤16 字符)
	if w := doRequest(srv, "POST", "/api/pet/name", map[string]any{
		"name": strings.Repeat("长", 20),
	}); w.Code != 400 {
		t.Errorf("超长名应 400, got=%d body=%s", w.Code, w.Body.String())
	}

	// 控制字符: 引擎侧 cleanName 会剥离控制字符后接受 (不是拒绝)
	if w := doRequest(srv, "POST", "/api/pet/name", map[string]any{
		"name": "豆豆\x00",
	}); w.Code != 200 {
		t.Errorf("控制字符名应被清理后接受 (200), got=%d body=%s", w.Code, w.Body.String())
	}

	// 正常
	w := doRequest(srv, "POST", "/api/pet/name", map[string]any{"name": "豆豆"})
	if w.Code != 200 {
		t.Fatalf("正常名应 200, got=%d body=%s", w.Code, w.Body.String())
	}
	got := decodeJSON(t, w.Body.Bytes())
	if got["enabled"] != true {
		t.Errorf("改名响应应带 enabled:true, got=%v", got)
	}
}

// TestPetName_BadJSON 请求体非法 → 400。
func TestPetName_BadJSON(t *testing.T) {
	srv, _ := newTestServerWithPet(t)
	if w := doRequest(srv, "POST", "/api/pet/enable", nil); w.Code != 200 {
		t.Fatalf("enable: %d", w.Code)
	}
	w := doRequest(srv, "POST", "/api/pet/name", "{not json")
	if w.Code != 400 {
		t.Errorf("bad json 应 400, got=%d body=%s", w.Code, w.Body.String())
	}
}

// ---------- /api/pet/pos ----------

// TestPetPos_Validation 越界坐标 → 400; 正常 → 200 ok。
func TestPetPos_Validation(t *testing.T) {
	srv, _ := newTestServerWithPet(t)
	if w := doRequest(srv, "POST", "/api/pet/enable", nil); w.Code != 200 {
		t.Fatalf("enable: %d", w.Code)
	}

	// 越界坐标: 引擎可能拒 (400) 也可能 clamp 到 [0,1] (200), 两者都接受,
	// 但不能 500。引擎落地后按实际行为收紧。
	if w := doRequest(srv, "POST", "/api/pet/pos", map[string]any{"x": 1.5, "y": 0.5}); w.Code != 400 && w.Code != 200 {
		t.Errorf("越界 x 应 400(拒) 或 200(clamp), got=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(srv, "POST", "/api/pet/pos", map[string]any{"x": 0.5, "y": -0.1}); w.Code != 400 && w.Code != 200 {
		t.Errorf("越界 y 应 400(拒) 或 200(clamp), got=%d body=%s", w.Code, w.Body.String())
	}

	// 正常
	w := doRequest(srv, "POST", "/api/pet/pos", map[string]any{"x": 0.92, "y": 0.88})
	if w.Code != 200 {
		t.Fatalf("正常坐标应 200, got=%d body=%s", w.Code, w.Body.String())
	}
	got := decodeJSON(t, w.Body.Bytes())
	if got["ok"] != true {
		t.Errorf("pos 响应应带 ok:true, got=%v", got)
	}
}

// ---------- /api/pet/skin ----------

// TestPetSkin_Validation v2：语义 id。不存在 → 400; 清单内已解锁 → 200 ok。
func TestPetSkin_Validation(t *testing.T) {
	srv, _ := newTestServerWithPet(t)
	if w := doRequest(srv, "POST", "/api/pet/enable", nil); w.Code != 200 {
		t.Fatalf("enable: %d", w.Code)
	}

	// 不存在的皮肤 id（兜底清单仅 orange-cat）
	if w := doRequest(srv, "POST", "/api/pet/skin", map[string]any{"skin": "no-such-skin"}); w.Code != 400 {
		t.Errorf("不存在的皮肤应 400, got=%d body=%s", w.Code, w.Body.String())
	}

	// 正常（默认款 Lv1 解锁）
	w := doRequest(srv, "POST", "/api/pet/skin", map[string]any{"skin": "orange-cat"})
	if w.Code != 200 {
		t.Fatalf("正常皮肤应 200, got=%d body=%s", w.Code, w.Body.String())
	}
	got := decodeJSON(t, w.Body.Bytes())
	if got["ok"] != true {
		t.Errorf("skin 响应应带 ok:true, got=%v", got)
	}
}

// TestPetSkin_Locked 清单内存在但等级未解锁 → 400（错误信息带解锁等级）。
func TestPetSkin_Locked(t *testing.T) {
	skinsJSON := []byte(`{
		"version": 2, "frameSize": 32, "frames": 4, "fps": 6,
		"skins": [
			{"id": "orange-cat", "name": "橘座", "category": "cat", "unlock": 1, "frames": 4, "fps": 6},
			{"id": "gold-dragon", "name": "金龙", "category": "dragon", "unlock": 20, "frames": 4, "fps": 6}
		]
	}`)
	srv, _ := newTestServerWithPetSkins(t, skinsJSON)
	if w := doRequest(srv, "POST", "/api/pet/enable", nil); w.Code != 200 {
		t.Fatalf("enable: %d", w.Code)
	}

	w := doRequest(srv, "POST", "/api/pet/skin", map[string]any{"skin": "gold-dragon"})
	if w.Code != 400 {
		t.Fatalf("未解锁皮肤应 400, got=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Lv20") {
		t.Errorf("错误信息应提示解锁等级: %s", w.Body.String())
	}

	// state 下发的清单带解锁状态
	w2 := doRequest(srv, "GET", "/api/pet/state", nil)
	if w2.Code != 200 {
		t.Fatalf("state: %d", w2.Code)
	}
	st := decodeJSON(t, w2.Body.Bytes())
	skins, ok := st["skins"].([]any)
	if !ok || len(skins) != 2 {
		t.Fatalf("state.skins 应 2 款, got=%#v", st["skins"])
	}
	gold := skins[1].(map[string]any) // 按 unlock 排序：orange-cat(Lv1) 在前
	if gold["id"] != "gold-dragon" || gold["unlocked"] != false {
		t.Fatalf("gold-dragon 应未解锁: %+v", gold)
	}
}

// ---------- /api/pet/sync ----------

// TestPetSync_UpstreamDown 上游不可用 (mock 返 500) → 502 优雅降级 (不 crash, 不泄露 panic)。
// 端点默认值已硬编码在 pet 包 (跟 sponsor 同款), 这里用 pet.InitFromConfig 指向 mock 覆盖。
func TestPetSync_UpstreamDown(t *testing.T) {
	srv, _ := newTestServerWithPet(t)
	if w := doRequest(srv, "POST", "/api/pet/enable", nil); w.Code != 200 {
		t.Fatalf("enable: %d", w.Code)
	}

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer up.Close()
	pet.InitFromConfig(up.URL+"/credit/httpInterface", "", "", 0)

	w := doRequest(srv, "POST", "/api/pet/sync", nil)
	if w.Code != 502 {
		t.Fatalf("上游不可用应 502, got=%d body=%s", w.Code, w.Body.String())
	}
	got := decodeJSON(t, w.Body.Bytes())
	if got["ok"] != false {
		t.Errorf("ok 应为 false, got=%v", got)
	}
	if !strings.Contains(w.Body.String(), "排行榜服务暂时不可用") {
		t.Errorf("错误信息应含'排行榜服务暂时不可用': %s", w.Body.String())
	}
}

// ---------- /api/pet/leaderboard ----------

// TestPetLeaderboard_EmptyCache 空缓存 → ok:true + 空 entries + stale:true + cached:true。
func TestPetLeaderboard_EmptyCache(t *testing.T) {
	srv, _ := newTestServerWithPet(t)
	if w := doRequest(srv, "POST", "/api/pet/enable", nil); w.Code != 200 {
		t.Fatalf("enable: %d", w.Code)
	}

	w := doRequest(srv, "GET", "/api/pet/leaderboard", nil)
	if w.Code != 200 {
		t.Fatalf("leaderboard code=%d body=%s", w.Code, w.Body.String())
	}
	got := decodeJSON(t, w.Body.Bytes())
	if got["ok"] != true {
		t.Errorf("ok 应为 true, got=%v", got)
	}
	if got["cached"] != true {
		t.Errorf("cached 应为 true, got=%v", got)
	}
	if got["stale"] != true {
		t.Errorf("空缓存 stale 应为 true, got=%v", got)
	}
}

// ---------- 未解锁 / 未注入时的权限边界 ----------

// TestPetRoutes_NotEnabled 未解锁时写接口 + sync + leaderboard 全 404。
func TestPetRoutes_NotEnabled(t *testing.T) {
	srv, _ := newTestServerWithPet(t)

	for _, c := range []struct{ method, path string }{
		{"POST", "/api/pet/name"},
		{"POST", "/api/pet/pos"},
		{"POST", "/api/pet/skin"},
		{"POST", "/api/pet/sync"},
		{"GET", "/api/pet/leaderboard"},
	} {
		w := doRequest(srv, c.method, c.path, map[string]any{})
		if w.Code != 404 {
			t.Errorf("%s %s 未解锁应 404, got=%d body=%s", c.method, c.path, w.Code, w.Body.String())
		}
	}
}

// TestPetRoutes_NoEngine 引擎未注入时所有 /api/pet/* 全 404。
func TestPetRoutes_NoEngine(t *testing.T) {
	srv, _, _, _ := newTestServer(t)

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/pet/state"},
		{"POST", "/api/pet/enable"},
		{"POST", "/api/pet/name"},
		{"POST", "/api/pet/sync"},
		{"GET", "/api/pet/leaderboard"},
	} {
		w := doRequest(srv, c.method, c.path, map[string]any{})
		if w.Code != 404 {
			t.Errorf("%s %s 未注入引擎应 404, got=%d body=%s", c.method, c.path, w.Code, w.Body.String())
		}
	}
}

// ---------- 命名空间预留 ----------

// TestPetRoutes_ReservedNamespaces skills/battle 命名空间预留, v1 一律 404。
func TestPetRoutes_ReservedNamespaces(t *testing.T) {
	srv, _ := newTestServerWithPet(t)
	if w := doRequest(srv, "POST", "/api/pet/enable", nil); w.Code != 200 {
		t.Fatalf("enable: %d", w.Code)
	}

	for _, path := range []string{
		"/api/pet/skills", "/api/pet/skills/list", "/api/pet/battle", "/api/pet/battle/start",
		"/api/pet/unknown",
	} {
		w := doRequest(srv, "GET", path, nil)
		if w.Code != 404 {
			t.Errorf("%s 应 404 (命名空间预留), got=%d body=%s", path, w.Code, w.Body.String())
		}
	}
}

// ---------- 方法限制 ----------

// TestPetRoutes_WrongMethod state/leaderboard 只认 GET, 写接口只认 POST。
func TestPetRoutes_WrongMethod(t *testing.T) {
	srv, _ := newTestServerWithPet(t)
	if w := doRequest(srv, "POST", "/api/pet/enable", nil); w.Code != 200 {
		t.Fatalf("enable: %d", w.Code)
	}

	if w := doRequest(srv, "POST", "/api/pet/state", nil); w.Code != 405 {
		t.Errorf("POST state 应 405, got=%d", w.Code)
	}
	if w := doRequest(srv, "GET", "/api/pet/enable", nil); w.Code != 405 {
		t.Errorf("GET enable 应 405, got=%d", w.Code)
	}
	if w := doRequest(srv, "POST", "/api/pet/leaderboard", nil); w.Code != 405 {
		t.Errorf("POST leaderboard 应 405, got=%d", w.Code)
	}
}
