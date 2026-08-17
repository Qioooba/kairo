// Package pet 实现隐藏彩蛋式的陪伴宠物（v0.16）。
//
// 设计要点：
//   - 未解锁（enabled=false）时引擎 no-op：不累计经验、不写盘、零痕迹；
//   - 经验复用 internal/audit 的订阅机制（audit.Subscriber），零新增埋点；
//   - 本地状态 data/pet.json 带 HMAC 签名（门槛，非防线），损坏自动回退 .bak；
//   - 排行榜只认服务器重算后的认可分（board_exp），本地数据永远不可信。
//
// 本文件：宠物状态数据结构 + 加载 / 保存（防抖写盘、HMAC、备份回退）。
package pet

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// StateVersion 状态文件格式版本号。
const StateVersion = 1

// Pos 浮动宠物位置（相对视口百分比，x/y ∈ [0,1]）。
type Pos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// OpStat 单个 op 的累计统计（次数 + 经验）。
type OpStat struct {
	Count int64 `json:"count"`
	Exp   int64 `json:"exp"`
}

// Stats 本地统计。Daily key "2006-01-02"，Monthly key "2006-01"，
// 均为 map[op]OpStat（键格式保证字典序即时间序，可直接按字符串比较清理）。
type Stats struct {
	Daily   map[string]map[string]OpStat `json:"daily"`
	Monthly map[string]map[string]OpStat `json:"monthly"`
	Total   map[string]OpStat            `json:"total"`
}

// LedgerEntry 一条未同步流水（只含 op/ts/exp，不含主机名/IP/路径，保护隐私）。
type LedgerEntry struct {
	Op  string `json:"op"`
	Ts  string `json:"ts"` // RFC3339 local
	Exp int64  `json:"exp"`
}

// Battle 预留：v2 对战三围（占位字段，v1 不填充）。
type Battle struct {
	Atk int `json:"atk"`
	Def int `json:"def"`
	HP  int `json:"hp"`
	Spd int `json:"spd"`
}

// State 宠物完整状态（data/pet.json 的磁盘结构）。
//
// 字段语义：
//   - Exp 是"当前等级内"经验；TotalEarned 才是历史累计（排行榜用）；
//   - Dirty 表示有未同步到服务器的流水（由 sync 流程回写清零，与落盘无关）；
//   - Sig 是 HMAC 签名（保存时生成，加载时校验），不参与签名载荷本身。
type State struct {
	V           int            `json:"v"`
	ID          string         `json:"id"`
	Enabled     bool           `json:"enabled"`
	Name        string         `json:"name"`
	Level       int            `json:"level"`
	Exp         int64          `json:"exp"` // 当前等级内经验
	Stage       string         `json:"stage"`
	Skin        int            `json:"skin"`
	Pos         Pos            `json:"pos"`
	TotalEarned int64          `json:"total_earned"`
	BoardExp    int64          `json:"board_exp"`
	Dirty       bool           `json:"dirty"`
	LastSync    string         `json:"last_sync"`
	Ledger      []LedgerEntry  `json:"ledger"`
	Stats       Stats          `json:"stats"`
	Speech      map[string]any `json:"speech"` // 预留
	Skills      []any          `json:"skills"` // 预留
	Battle      Battle         `json:"battle"` // 预留
	Sig         string         `json:"sig,omitempty"`
}

// freshState 构造全新的初始宠物（蛋阶段、Lv1、默认名"小K"、皮肤 0）。
func freshState() *State {
	return &State{
		V:       StateVersion,
		ID:      newID(),
		Enabled: false,
		Name:    "小K",
		Level:   1,
		Exp:     0,
		Stage:   "egg",
		Skin:    0,
		Pos:     Pos{X: 0.92, Y: 0.88},
		Stats: Stats{
			Daily:   make(map[string]map[string]OpStat),
			Monthly: make(map[string]map[string]OpStat),
			Total:   make(map[string]OpStat),
		},
		Ledger: make([]LedgerEntry, 0),
		Speech: make(map[string]any),
		Skills: make([]any, 0),
		Battle: Battle{},
	}
}

// newID 生成本机宠物标识（32 hex 字符随机串；无外部依赖，不引入 uuid 库）。
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败几乎不可能；兜底用时间戳避免返回空串
		return fmt.Sprintf("%016x%016x", uint64(os.Getpid()), uint64(time.Now().UnixNano()))
	}
	return hex.EncodeToString(b)
}

// normalizeState 防御性兜底：修复加载/构造后可能缺失的字段，
// 保证引擎内部永远面对"结构完整"的状态。
func normalizeState(st *State) {
	if st == nil {
		return
	}
	if st.V == 0 {
		st.V = StateVersion
	}
	if st.ID == "" {
		st.ID = newID()
	}
	if st.Level < 1 {
		st.Level = 1
	}
	if st.Exp < 0 {
		st.Exp = 0
	}
	if st.Name == "" {
		st.Name = "小K"
	}
	if st.TotalEarned < 0 {
		st.TotalEarned = 0
	}
	if math.IsNaN(st.Pos.X) || math.IsInf(st.Pos.X, 0) || st.Pos.X < 0 || st.Pos.X > 1 {
		st.Pos.X = 0.92
	}
	if math.IsNaN(st.Pos.Y) || math.IsInf(st.Pos.Y, 0) || st.Pos.Y < 0 || st.Pos.Y > 1 {
		st.Pos.Y = 0.88
	}
	if st.Stats.Daily == nil {
		st.Stats.Daily = make(map[string]map[string]OpStat)
	}
	if st.Stats.Monthly == nil {
		st.Stats.Monthly = make(map[string]map[string]OpStat)
	}
	if st.Stats.Total == nil {
		st.Stats.Total = make(map[string]OpStat)
	}
	if st.Ledger == nil {
		st.Ledger = make([]LedgerEntry, 0)
	}
	if st.Speech == nil {
		st.Speech = make(map[string]any)
	}
	if st.Skills == nil {
		st.Skills = make([]any, 0)
	}
}

// ---------- HMAC 签名 ----------

// signState 计算状态的 HMAC-SHA256 签名（hex 字符串）。
// payload 必须是 Sig="" 时 marshal 出的 JSON（签名载荷排除 sig 自身）。
func signState(payload, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// statePayload 序列化状态（浅拷贝后 Sig 置空），返回签名载荷与完整 JSON。
// 签名载荷刻意排除 Sig 字段，保证"签名验证"与"字段取值"互不干扰。
func statePayload(st *State) (payload []byte, full []byte, err error) {
	cp := *st
	cp.Sig = ""
	payload, err = json.Marshal(&cp)
	if err != nil {
		return nil, nil, fmt.Errorf("pet: 序列化状态失败: %w", err)
	}
	full, err = json.Marshal(st)
	if err != nil {
		return nil, nil, fmt.Errorf("pet: 序列化状态失败: %w", err)
	}
	return payload, full, nil
}

// verifyState 校验磁盘状态的签名。返回 nil 表示签名不匹配或格式损坏。
func verifyState(raw []byte, key []byte) (*State, error) {
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("pet: 解析状态失败: %w", err)
	}
	sig := st.Sig
	st.Sig = ""
	payload, err := json.Marshal(&st)
	if err != nil {
		return nil, fmt.Errorf("pet: 重新序列化失败: %w", err)
	}
	want := signState(payload, key)
	// 常量时间比较，避免侧信道
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return nil, errors.New("pet: 状态签名不匹配（文件可能被篡改）")
	}
	return &st, nil
}

// ---------- 密钥管理 ----------

// loadOrCreateSigKey 读取或生成 HMAC 密钥（跟随 internal/credentials 的 .credkey 模式）：
//   - keyPath 存在且合法（64 hex / 32 字节）→ 复用；
//   - 损坏 → 删除重建；
//   - 不存在 → crypto/rand 生成 32 字节，以 hex 写入 keyPath（0600）。
func loadOrCreateSigKey(keyPath string) ([]byte, error) {
	raw, err := os.ReadFile(keyPath)
	if err == nil {
		decoded, derr := hex.DecodeString(strings.TrimSpace(string(raw)))
		if derr == nil && len(decoded) == 32 {
			return decoded, nil
		}
		// 旧 key 损坏：删除重建（与 credentials 行为一致）
		_ = os.Remove(keyPath)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("pet: 生成随机密钥失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		return nil, fmt.Errorf("pet: 创建数据目录失败: %w", err)
	}
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("pet: 写入密钥文件失败: %w", err)
	}
	return key, nil
}

// ---------- 加载 / 保存 ----------

// loadStateFile 从 path 读取并校验状态；文件不存在 / 损坏 / 签名不匹配都返回 error。
func loadStateFile(path string, key []byte) (*State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	st, err := verifyState(raw, key)
	if err != nil {
		return nil, err
	}
	normalizeState(st)
	return st, nil
}

// saveStateFile 原子写盘：先写 <path>.tmp 再 rename；成功后把上一版复制为 <path>.bak。
// 备份失败不阻断保存（best-effort，主文件已经安全落盘）。
func saveStateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("pet: 创建数据目录失败: %w", err)
	}
	// 备份上一版（存在才备份；失败仅提示，不阻断）
	if _, err := os.Stat(path); err == nil {
		if err := copyFile(path, path+".bak"); err != nil {
			fmt.Fprintf(os.Stderr, "pet: 备份状态文件失败: %v\n", err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("pet: 写临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("pet: 原子替换状态文件失败: %w", err)
	}
	return nil
}

// copyFile 复制 src → dst（小文件，直接读全量）。
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

// ---------- 名称 / 位置校验 ----------

// maxNameRunes 宠物名字最大长度（按 rune 计，兼容中文）。
const maxNameRunes = 16

// cleanName 清理宠物名：去除控制字符 + TrimSpace。返回清理后的名字。
func cleanName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	return strings.TrimSpace(name)
}

// validateName 校验宠物名：非空且 ≤16 个 rune。
func validateName(name string) error {
	if name == "" {
		return errors.New("pet: 名字不能为空")
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		return fmt.Errorf("pet: 名字最长 %d 个字符", maxNameRunes)
	}
	return nil
}

// clamp01 把浮点数夹到 [0,1]。
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
