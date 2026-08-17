package pet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadSkins_Fallback 空数据/坏 JSON/空清单都退回兜底清单（不 error、恒非 nil）。
func TestLoadSkins_Fallback(t *testing.T) {
	for name, data := range map[string][]byte{
		"nil":       nil,
		"empty":     {},
		"garbage":   []byte("not json"),
		"no-skins":  []byte(`{"version":2,"skins":[]}`),
		"all-blank": []byte(`{"version":2,"skins":[{"id":"","name":"空id"}]}`),
	} {
		c := LoadSkins(data)
		if c == nil {
			t.Fatalf("%s: LoadSkins 不应返回 nil", name)
		}
		all := c.All()
		if len(all) != 1 || all[0].ID != DefaultSkinID {
			t.Fatalf("%s: 应退回兜底清单（仅 %q）, 实际 %+v", name, DefaultSkinID, all)
		}
		if c.FrameSize != 32 || c.Frames != 4 {
			t.Fatalf("%s: 兜底元信息异常: %+v", name, c.MetaView())
		}
	}
}

// TestLoadSkins_Valid 正常加载：归一化（unlock/frames/fps 兜底）、去重、稳定排序。
func TestLoadSkins_Valid(t *testing.T) {
	c := LoadSkins([]byte(`{
		"version": 2, "frameSize": 32, "frames": 4, "fps": 6,
		"skins": [
			{"id": "gold-dragon", "name": "金龙", "category": "dragon", "unlock": 20},
			{"id": "orange-cat", "name": "橘座", "category": "cat", "unlock": 0},
			{"id": "orange-cat", "name": "橘座重复", "category": "cat", "unlock": 1},
			{"id": "blue-cat", "name": "蓝猫", "category": "cat", "unlock": 1, "frames": 0, "fps": 0}
		]
	}`))
	all := c.All()
	if len(all) != 3 {
		t.Fatalf("重复 id 应去重, 实际 %d 款: %+v", len(all), all)
	}
	// 稳定排序：unlock 升序，同级按 id（blue-cat < orange-cat）
	wantOrder := []string{"blue-cat", "orange-cat", "gold-dragon"}
	for i, want := range wantOrder {
		if all[i].ID != want {
			t.Fatalf("排序[%d] = %q, 期望 %q", i, all[i].ID, want)
		}
	}
	// 归一化：unlock<1 → 1；frames/fps 缺省继承清单级
	oc, _ := c.Get("orange-cat")
	if oc.Unlock != 1 || oc.Frames != 4 || oc.FPS != 6 {
		t.Fatalf("orange-cat 归一化异常: %+v", oc)
	}
	bc, _ := c.Get("blue-cat")
	if bc.Frames != 4 || bc.FPS != 6 {
		t.Fatalf("blue-cat 帧数/帧率应兜底清单级: %+v", bc)
	}
}

// TestLoadSkins_Unlock 解锁判断边界。
func TestLoadSkins_Unlock(t *testing.T) {
	c := LoadSkins([]byte(`{
		"skins": [{"id": "a", "name": "A", "unlock": 4}, {"id": "b", "name": "B", "unlock": 1}]
	}`))
	if !c.IsUnlocked("a", 4) {
		t.Fatal("Lv4 应解锁 unlock=4 的皮肤（含边界）")
	}
	if c.IsUnlocked("a", 3) {
		t.Fatal("Lv3 不应解锁 unlock=4 的皮肤")
	}
	if !c.IsUnlocked("b", 1) {
		t.Fatal("Lv1 应解锁 unlock=1 的皮肤")
	}
	if c.IsUnlocked("no-such", 99) {
		t.Fatal("不存在的皮肤永远未解锁")
	}
}

// TestMigrateLegacySkin v1 int 索引 → v2 语义 id。
func TestMigrateLegacySkin(t *testing.T) {
	cases := []struct {
		raw     string
		wantID  string
		migrate bool
	}{
		{`0`, "orange-cat", true},
		{`1`, "blue-cat", true},
		{`5`, DefaultSkinID, true},              // 越界 → 默认款
		{`-1`, DefaultSkinID, true},             // 负数 → 默认款
		{`"orange-cat"`, `"orange-cat"`, false}, // 已是 v2 字符串：原样返回
	}
	for _, c := range cases {
		id, migrated := MigrateLegacySkin([]byte(c.raw))
		if id != c.wantID || migrated != c.migrate {
			t.Fatalf("MigrateLegacySkin(%s) = (%q,%v), 期望 (%q,%v)", c.raw, id, migrated, c.wantID, c.migrate)
		}
	}
}

// TestMigrateStateFile 磁盘 v1 状态（skin 为 int）加载时自动迁移且可用。
func TestMigrateStateFile(t *testing.T) {
	// 手工构造 v1 状态文件：皮肤 1（blue-cat），无有效签名
	dir := t.TempDir()
	v1 := `{"v":1,"id":"pet-v1","enabled":true,"name":"旧宠物","level":2,"exp":10,
		"stage":"egg","skin":1,"pos":{"x":0.5,"y":0.5},"total_earned":90,
		"ledger":[],"stats":{"daily":{},"monthly":{},"total":{}}}`
	if err := os.WriteFile(filepath.Join(dir, "pet.json"), []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(DefaultRules(), filepath.Join(dir, "pet.json"), nil, nil)
	if err != nil {
		t.Fatalf("v1 状态迁移加载失败: %v", err)
	}
	defer e.Close()
	st := e.State()
	if !st.Enabled {
		t.Fatal("迁移后应保持已解锁")
	}
	if st.Skin != "blue-cat" {
		t.Fatalf("v1 skin=1 应迁移为 blue-cat, 实际 %q", st.Skin)
	}
}

// TestValidateSkin 错误信息可直接透传 API 400。
func TestValidateSkin(t *testing.T) {
	c := LoadSkins([]byte(`{"skins":[{"id":"a","name":"A","unlock":4}]}`))
	if err := c.ValidateSkin("a", 4); err != nil {
		t.Fatalf("已解锁应通过: %v", err)
	}
	err := c.ValidateSkin("a", 3)
	if err == nil {
		t.Fatal("未解锁应报错")
	}
	if !strings.Contains(err.Error(), "Lv4") {
		t.Fatalf("错误应含解锁等级: %v", err)
	}
	if err := c.ValidateSkin("zzz", 99); err == nil {
		t.Fatal("不存在的皮肤应报错")
	}
}

// TestCatalogView 下发视图：解锁标志按等级填充。
func TestCatalogView(t *testing.T) {
	c := LoadSkins([]byte(`{
		"skins": [
			{"id":"a","name":"A","category":"cat","unlock":1},
			{"id":"b","name":"B","category":"dragon","unlock":5}
		]
	}`))
	view := c.CatalogView(5)
	if len(view) != 2 {
		t.Fatalf("视图应 2 款, 实际 %d", len(view))
	}
	if view[0]["id"] != "a" || view[0]["unlocked"] != true {
		t.Fatalf("a 应已解锁: %+v", view[0])
	}
	if view[1]["id"] != "b" || view[1]["unlocked"] != true {
		t.Fatalf("b 在 Lv5 应解锁（含边界）: %+v", view[1])
	}
	view4 := c.CatalogView(4)
	if view4[1]["unlocked"] != false {
		t.Fatalf("b 在 Lv4 应未解锁: %+v", view4[1])
	}
}

// TestLoadSkins_RealManifest 真实清单回归：源码树内的 web/img/pet/skins/skins.json
// 必须能被 LoadSkins 正常加载，且每款皮肤的 PNG 精灵图都存在
// （防 v1「skin_count 与文件脱节 → 前端 404 破图」复发）。
func TestLoadSkins_RealManifest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "web", "img", "pet", "skins", "skins.json"))
	if err != nil {
		t.Skip("源码树外构建，跳过真实清单检查: ", err)
	}
	c := LoadSkins(data)
	all := c.All()
	if len(all) < 50 {
		t.Fatalf("真实清单应 ≥50 款, 实际 %d", len(all))
	}
	if _, ok := c.Get(DefaultSkinID); !ok {
		t.Fatalf("默认皮肤 %q 必须在清单中", DefaultSkinID)
	}
	for _, s := range all {
		if s.ID == "" || s.Name == "" || s.Unlock < 1 || s.Frames < 1 || s.FPS < 1 {
			t.Fatalf("清单字段异常: %+v", s)
		}
		png := filepath.Join("..", "..", "web", "img", "pet", "skins", s.ID+".png")
		if _, err := os.Stat(png); err != nil {
			t.Fatalf("皮肤 PNG 缺失（清单与文件脱节）: %s", png)
		}
	}
}
