// Package pet - 皮肤清单（v2）。
//
// 皮肤资源由 tools/petgen 程序化生成（见 docs/PET-SKINS-V2-DESIGN.md §1）：
//   - 精灵图：web/img/pet/skins/{id}.png（4 帧横排 128×32，随二进制 embed 分发）
//   - 清单：web/img/pet/skins/skins.json（本文件负责加载/校验/解锁判断）
//
// 引擎持有 SkinCatalog；SetSkin 的存在性与解锁校验都以清单为准，
// 彻底取代 v1 的 config.pet.skin_count 手动配置（清单与文件永不脱节）。
package pet

import (
	"encoding/json"
	"fmt"
	"sort"
)

// DefaultSkinID 默认皮肤（清单缺失/新宠物的兜底款：橘座）。
const DefaultSkinID = "orange-cat"

// legacySkinIDs v1 时代 skin 字段是 int 索引（仅 2 款），按下标映射到 v2 语义 id。
var legacySkinIDs = []string{"orange-cat", "blue-cat"}

// SkinDef 单款皮肤定义（skins.json 条目）。
type SkinDef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Unlock   int    `json:"unlock"` // 解锁等级（≥1）
	Frames   int    `json:"frames"` // 精灵图帧数
	FPS      int    `json:"fps"`    // 建议播放帧率
}

// SkinCatalog 皮肤清单（加载后只读）。
type SkinCatalog struct {
	Version   int
	FrameSize int
	Frames    int
	FPS       int

	defs []SkinDef
	byID map[string]SkinDef
}

// fallbackCatalog 清单不可用时的最小兜底（只有默认款，保证引擎可用）。
func fallbackCatalog() *SkinCatalog {
	return &SkinCatalog{
		Version: 2, FrameSize: 32, Frames: 4, FPS: 6,
		defs: []SkinDef{{ID: DefaultSkinID, Name: "橘座", Category: "cat", Unlock: 1, Frames: 4, FPS: 6}},
		byID: map[string]SkinDef{DefaultSkinID: {ID: DefaultSkinID, Name: "橘座", Category: "cat", Unlock: 1, Frames: 4, FPS: 6}},
	}
}

// LoadSkins 从 skins.json 字节加载清单。
//
// 任何失败（空数据/解析错/重复 id/空清单）都不返回 error，而是退回
// fallbackCatalog：皮肤是彩蛋功能，清单坏不该阻断宠物引擎启动。
// 返回的清单恒非 nil。
func LoadSkins(data []byte) *SkinCatalog {
	if len(data) == 0 {
		return fallbackCatalog()
	}
	var raw struct {
		Version   int       `json:"version"`
		FrameSize int       `json:"frameSize"`
		Frames    int       `json:"frames"`
		FPS       int       `json:"fps"`
		Skins     []SkinDef `json:"skins"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fallbackCatalog()
	}
	if len(raw.Skins) == 0 {
		return fallbackCatalog()
	}
	c := &SkinCatalog{
		Version:   raw.Version,
		FrameSize: raw.FrameSize,
		Frames:    raw.Frames,
		FPS:       raw.FPS,
		defs:      make([]SkinDef, 0, len(raw.Skins)),
		byID:      make(map[string]SkinDef, len(raw.Skins)),
	}
	for _, s := range raw.Skins {
		if s.ID == "" {
			continue
		}
		if _, dup := c.byID[s.ID]; dup {
			continue // 重复 id 只留首个
		}
		if s.Unlock < 1 {
			s.Unlock = 1
		}
		if s.Frames <= 0 {
			s.Frames = raw.Frames
		}
		if s.Frames <= 0 {
			s.Frames = 4
		}
		if s.FPS <= 0 {
			s.FPS = raw.FPS
		}
		if s.FPS <= 0 {
			s.FPS = 6
		}
		c.byID[s.ID] = s
		c.defs = append(c.defs, s)
	}
	if len(c.defs) == 0 {
		return fallbackCatalog()
	}
	// 按解锁等级稳定排序（同级按 id），保证下发给前端的顺序确定
	sort.SliceStable(c.defs, func(i, j int) bool {
		if c.defs[i].Unlock != c.defs[j].Unlock {
			return c.defs[i].Unlock < c.defs[j].Unlock
		}
		return c.defs[i].ID < c.defs[j].ID
	})
	return c
}

// All 返回全部皮肤（按解锁等级排序的拷贝）。
func (c *SkinCatalog) All() []SkinDef {
	out := make([]SkinDef, len(c.defs))
	copy(out, c.defs)
	return out
}

// Get 按 id 查皮肤。
func (c *SkinCatalog) Get(id string) (SkinDef, bool) {
	s, ok := c.byID[id]
	return s, ok
}

// IsUnlocked 等级是否达到皮肤解锁线。
func (c *SkinCatalog) IsUnlocked(id string, level int) bool {
	s, ok := c.byID[id]
	if !ok {
		return false
	}
	return level >= s.Unlock
}

// MigrateLegacySkin 把 v1 的 int 皮肤索引迁移为 v2 语义 id。
// 输入是 pet.json 里 skin 字段的原始 JSON；非数字（已是 string）返回原值与 false。
func MigrateLegacySkin(raw json.RawMessage) (string, bool) {
	var n float64
	if err := json.Unmarshal(raw, &n); err != nil {
		return string(raw), false
	}
	idx := int(n)
	if idx >= 0 && idx < len(legacySkinIDs) {
		return legacySkinIDs[idx], true
	}
	return DefaultSkinID, true
}

// SkinView /api/pet/state 下发的单款皮肤视图（unlocked 由调用方按当前等级填）。
func SkinView(s SkinDef, unlocked bool) map[string]any {
	return map[string]any{
		"id":       s.ID,
		"name":     s.Name,
		"category": s.Category,
		"unlock":   float64(s.Unlock),
		"frames":   float64(s.Frames),
		"fps":      float64(s.FPS),
		"unlocked": unlocked,
	}
}

// CatalogView 整个清单的下发视图（全部皮肤 + 每款解锁状态）。
func (c *SkinCatalog) CatalogView(level int) []map[string]any {
	out := make([]map[string]any, 0, len(c.defs))
	for _, s := range c.defs {
		out = append(out, SkinView(s, level >= s.Unlock))
	}
	return out
}

// MetaView 精灵图元信息（前端渲染用）。
func (c *SkinCatalog) MetaView() map[string]any {
	return map[string]any{
		"version":   float64(c.Version),
		"frameSize": float64(c.FrameSize),
		"frames":    float64(c.Frames),
		"fps":       float64(c.FPS),
	}
}

// ValidateSkin 供 SetSkin：存在 + 解锁。错误信息可直接透传给 API 400。
func (c *SkinCatalog) ValidateSkin(id string, level int) error {
	s, ok := c.byID[id]
	if !ok {
		return fmt.Errorf("pet: 皮肤不存在（%q）", id)
	}
	if level < s.Unlock {
		return fmt.Errorf("pet: 该皮肤需要 Lv%d 解锁（当前 Lv%d）", s.Unlock, level)
	}
	return nil
}
