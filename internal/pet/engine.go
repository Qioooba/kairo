package pet

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"kairo/internal/audit"
)

// saveDebounce 防抖落盘间隔：状态变化后静默 30s 才写盘（Close/Enable 立即写）。
const saveDebounce = 30 * time.Second

// sessionStaleAfter 会话表清理阈值：开始时间超过 24h 视为孤儿会话。
const sessionStaleAfter = 24 * time.Hour

// sessionAwardCap 单会话时长奖励封顶（+8 经验，v2 从 5 上调）。
const sessionAwardCap = int64(8)

// sessionMinDuration 会话真实性门槛：时长不足 3min 的会话不计时长奖励
// （防"秒开秒关"刷 ssh.shell.start + 会话分）。
const sessionMinDuration = 3 * time.Minute

// rapidRepeatWindow 频次异常窗口：同 op+target 两次计分间隔小于该值视为脚本
// 行为，本次丢弃并重置该 key 冷却（惩罚）。
const rapidRepeatWindow = 2 * time.Second

// Engine 宠物引擎：订阅 audit 事件、累计经验、维护状态并防抖落盘。
//
// 线程安全：全部字段由 mu 保护；enabledFlag 是原子布尔，
// 供 OnOp 第一行做 no-op 快门（未解锁时 ~1ns 返回，零记录）。
type Engine struct {
	mu       sync.Mutex
	rules    Rules
	dataPath string
	sigKey   []byte
	skins    *SkinCatalog

	// idSource 可选：新建宠物时用其返回值作稳定 ID（如从激活码派生），
	// 保证同一用户重装/删 pet.json 后仍是同一只宠物（排行榜不会出现多个自己）。
	// 仅对"新建"生效；已存在的 pet.json 保持原 ID，避免老用户排行榜分裂。
	idSource func() (string, bool)

	// codeSource 可选：返回激活码（用于同步时上送服务器，服务器据此做
	// 激活码唯一绑定 / user_id 校验）。
	codeSource func() (string, bool)

	state *State

	enabledFlag atomic.Bool

	// 当日累计（内存态，跨天重置，不持久化）
	todayKey      string
	todayEarned   int64
	dailyOpCounts map[string]int64

	// 冷却表 key=op|target → 最近一次计分时间；会话表 key=target → 会话开始时间
	cooldowns map[string]time.Time
	sessions  map[string]time.Time

	// 防刷：最近一次计分时间（同 cooldowns 的 key），用于 2s 频次异常检测
	lastAward map[string]time.Time

	// 防抖落盘控制
	needSave  bool
	dirtyCh   chan struct{}
	stopCh    chan struct{}
	doneCh    chan struct{}
	closeOnce sync.Once
}

// NewEngine 构造引擎；立即从 dataPath 加载已有状态（含 enabled/等级/统计）。
//
// skinsJSON 是 web/img/pet/skins/skins.json 的内容（随二进制 embed），
// 传 nil/空时退回内置最小清单（见 LoadSkins）。
//
// 加载失败（文件损坏 / 签名不匹配）不返回错误：先回退 <path>.bak，
// 仍失败则新建状态（日志写 stderr）。只有密钥生成失败 / 数据目录不可写才返回 error。
//
// sigKey 为空时：读取 <pet.json 所在目录>/.petkey，不存在则生成 32 随机字节写入
// （模式 0600，目录 0755，跟随 internal/credentials 的 .credkey 模式）。
func NewEngine(rules Rules, dataPath string, sigKey []byte, skinsJSON []byte) (*Engine, error) {
	return newEngine(rules, dataPath, sigKey, skinsJSON, nil, nil)
}

// NewEngineWithIDSource 同 NewEngine，额外指定新建宠物时的稳定 ID 来源
// （例如从激活码派生，见 idSource 字段注释）。
func NewEngineWithIDSource(rules Rules, dataPath string, sigKey []byte, skinsJSON []byte, idSource func() (string, bool)) (*Engine, error) {
	return newEngine(rules, dataPath, sigKey, skinsJSON, idSource, nil)
}

// NewEngineWithSources 同 NewEngine，同时指定稳定 ID 来源与激活码来源
// （激活码用于同步时上送服务器做唯一绑定，见 codeSource 字段注释）。
func NewEngineWithSources(rules Rules, dataPath string, sigKey []byte, skinsJSON []byte, idSource, codeSource func() (string, bool)) (*Engine, error) {
	return newEngine(rules, dataPath, sigKey, skinsJSON, idSource, codeSource)
}

func newEngine(rules Rules, dataPath string, sigKey []byte, skinsJSON []byte, idSource, codeSource func() (string, bool)) (*Engine, error) {
	if dataPath == "" {
		return nil, errors.New("pet: dataPath 不能为空")
	}
	dir := filepath.Dir(dataPath)
	key := sigKey
	if len(key) == 0 {
		var err error
		key, err = loadOrCreateSigKey(filepath.Join(dir, ".petkey"))
		if err != nil {
			return nil, err
		}
	}
	// 数据目录可写性预检（不创建 pet.json，保证未解锁零痕迹）
	if err := checkWritable(dir); err != nil {
		return nil, err
	}

	e := &Engine{
		rules:         normalizeRules(rules),
		dataPath:      dataPath,
		sigKey:        key,
		skins:         LoadSkins(skinsJSON),
		idSource:      idSource,
		codeSource:    codeSource,
		cooldowns:     make(map[string]time.Time),
		sessions:      make(map[string]time.Time),
		lastAward:     make(map[string]time.Time),
		dailyOpCounts: make(map[string]int64),
		dirtyCh:       make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}

	// 加载（失败静默回退，不阻断启动）
	st, err := loadStateFile(dataPath, key)
	if err != nil && !os.IsNotExist(err) {
		// 文件存在但损坏/签名不匹配：尝试 .bak；再失败才新建
		if bak, berr := loadStateFile(dataPath+".bak", key); berr == nil {
			fmt.Fprintf(os.Stderr, "pet: 主状态文件损坏（%v），已回退备份\n", err)
			st = bak
		} else {
			fmt.Fprintf(os.Stderr, "pet: 状态文件与备份均不可用（%v），新建宠物\n", err)
		}
	}
	if st == nil {
		st = freshState()
		// 有稳定 ID 来源（如激活码）时用派生 ID，保证重装后仍是同一只宠物。
		if e.idSource != nil {
			if id, ok := e.idSource(); ok && id != "" {
				st.ID = id
			}
		}
	}
	normalizeState(st)
	e.state = st
	if st.Enabled {
		e.enabledFlag.Store(true)
	}

	go e.saveLoop()
	return e, nil
}

// normalizeRules 兜底规则中的零值字段（0 视为"未配置"，用默认补齐）。
// 只动标量字段；map 与 DailyCap/CooldownMinutes 保持原样（0 有明确语义）。
func normalizeRules(r Rules) Rules {
	d := DefaultRules()
	if r.MaxLedger <= 0 {
		r.MaxLedger = d.MaxLedger
	}
	if r.SessionExpMinutes <= 0 {
		r.SessionExpMinutes = d.SessionExpMinutes
	}
	if r.StatsKeepDays <= 0 {
		r.StatsKeepDays = d.StatsKeepDays
	}
	if r.StatsKeepMonths <= 0 {
		r.StatsKeepMonths = d.StatsKeepMonths
	}
	if r.ExpRules == nil {
		r.ExpRules = d.ExpRules
	}
	if r.StageLevels == nil {
		r.StageLevels = d.StageLevels
	}
	if r.OpDailyMax == nil {
		r.OpDailyMax = d.OpDailyMax
	}
	return r
}

// checkWritable 验证 dir 可创建且可写（用临时文件探测后立即删除）。
func checkWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("pet: 创建数据目录失败: %w", err)
	}
	f, err := os.CreateTemp(dir, ".pet-write-test-*")
	if err != nil {
		return fmt.Errorf("pet: 数据目录不可写: %w", err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// ---------- 开关 ----------

// enabled 原子读：OnOp 第一行的 no-op 快门（未解锁零记录）。
func (e *Engine) enabled() bool {
	return e.enabledFlag.Load()
}

// Enabled 是否已解锁（未解锁时 OnOp 零记录）。
func (e *Engine) Enabled() bool {
	return e.enabled()
}

// Enable 用户解锁（点击 6 次关于）：幂等，标记 enabled=true，立即 Save。
// 解锁前引擎一直处于 no-op，解锁前的操作一律不追溯。
func (e *Engine) Enable() (*State, error) {
	return e.forceEnable()
}

// ForceEnable 开发者强制解锁（config pet.enabled=true）：同 Enable。
func (e *Engine) ForceEnable() (*State, error) {
	return e.forceEnable()
}

// forceEnable 解锁公共实现：幂等 + 立即落盘。
func (e *Engine) forceEnable() (*State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.state.Enabled {
		e.state.Enabled = true
		e.state.Stage = stageForLevel(e.rules.StageLevels, e.state.Level)
		e.enabledFlag.Store(true)
	}
	e.needSave = true
	if err := e.saveLocked(); err != nil {
		return nil, err
	}
	return e.stateCopyLocked(), nil
}

// ---------- 状态读写 ----------

// State 返回状态的深拷贝（JSON 往返，调用方随便改都不影响引擎内部）。
func (e *Engine) State() *State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stateCopyLocked()
}

// stateCopyLocked 深拷贝当前状态（调用方必须已持锁）。
func (e *Engine) stateCopyLocked() *State {
	b, err := json.Marshal(e.state)
	if err != nil {
		// 状态字段都是 JSON-safe 类型，序列化失败几乎不可能
		return freshState()
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return freshState()
	}
	return &st
}

// StateView 供 HTTP 返回：State 拷贝 + 计算字段。
// JSON 字段名：next_exp（下一级所需经验阈值）、today_earned（今日已得）、
// daily_cap（每日上限）、skins（皮肤清单 + 每款解锁状态）、skin_meta（精灵图元信息）。
func (e *Engine) StateView() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, err := json.Marshal(e.state)
	if err != nil {
		return map[string]any{"enabled": e.state.Enabled}
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m == nil {
		m = make(map[string]any)
	}
	delete(m, "sig") // HMAC 签名不下发前端
	today := time.Now().Format("2006-01-02")
	earned := int64(0)
	if e.todayKey == today {
		earned = e.todayEarned
	}
	m["next_exp"] = float64(NextExp(e.state.Level))
	m["today_earned"] = float64(earned)
	m["daily_cap"] = float64(e.rules.DailyCap)
	m["skins"] = e.skins.CatalogView(e.state.Level)
	m["skin_meta"] = e.skins.MetaView()
	return m
}

// Rename 改名：校验 ≤16 个 rune、去除控制字符、TrimSpace；空名拒绝。
func (e *Engine) Rename(name string) error {
	cleaned := cleanName(name)
	if err := validateName(cleaned); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.Name == cleaned {
		return nil
	}
	e.state.Name = cleaned
	e.markDirtyLocked()
	return nil
}

// SetOwner 设置主人的名字（宠物闲聊时称呼）。允许空串（未配置时话术回退「主人」），
// 非空时长度规则与改名一致。
func (e *Engine) SetOwner(name string) error {
	cleaned := cleanName(name)
	if cleaned != "" {
		if err := validateName(cleaned); err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.Owner == cleaned {
		return nil
	}
	e.state.Owner = cleaned
	e.markDirtyLocked()
	return nil
}

// SetPos 保存浮动宠物位置：x/y clamp 到 [0,1]，非法 NaN/Inf 拒绝。
func (e *Engine) SetPos(x, y float64) error {
	if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
		return errors.New("pet: 位置必须是有限数值")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	x = clamp01(x)
	y = clamp01(y)
	if e.state.Pos.X == x && e.state.Pos.Y == y {
		return nil
	}
	e.state.Pos.X = x
	e.state.Pos.Y = y
	e.markDirtyLocked()
	return nil
}

// SetSkin 保存皮肤（v2：语义 id）。校验：清单内存在 + 当前等级已解锁。
func (e *Engine) SetSkin(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.skins.ValidateSkin(id, e.state.Level); err != nil {
		return err
	}
	if e.state.Skin == id {
		return nil
	}
	e.state.Skin = id
	e.markDirtyLocked()
	return nil
}

// Skins 返回皮肤清单（只读视图拷贝）。
func (e *Engine) Skins() *SkinCatalog {
	return e.skins
}

// ---------- 防抖落盘 ----------

// markDirtyLocked 标记需要落盘并触发防抖定时器（调用方必须已持锁）。
func (e *Engine) markDirtyLocked() {
	e.needSave = true
	e.signalSaveLocked()
}

// signalSaveLocked 非阻塞唤醒 saveLoop（调用方必须已持锁）。
func (e *Engine) signalSaveLocked() {
	select {
	case e.dirtyCh <- struct{}{}:
	default:
	}
}

// saveLoop 防抖落盘循环：收到变化信号后静默 saveDebounce 才写盘。
// 期间再次变化会重置计时（"quiet 30s"语义）；stopCh 关闭即退出。
func (e *Engine) saveLoop() {
	defer close(e.doneCh)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-e.dirtyCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(saveDebounce)
		case <-timer.C:
			_ = e.save()
		}
	}
}

// save 立即尝试落盘（needSave=false 时跳过）。线程安全。
func (e *Engine) save() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.saveLocked()
}

// saveLocked 序列化 + 签名 + 原子写盘（调用方必须已持锁）。
//
// 写盘流程：marshal(Sig 置空) 得签名载荷 → 算 HMAC → 填 Sig →
// 写 <path>.tmp → rename 到 <path>（上一版已在 saveStateFile 内复制为 .bak）。
// 成功后 needSave=false。
func (e *Engine) saveLocked() error {
	if !e.needSave {
		return nil
	}
	payload, _, err := statePayload(e.state)
	if err != nil {
		return err
	}
	e.state.Sig = signState(payload, e.sigKey)
	full, err := json.Marshal(e.state)
	if err != nil {
		return fmt.Errorf("pet: 序列化状态失败: %w", err)
	}
	if err := saveStateFile(e.dataPath, full); err != nil {
		return err
	}
	e.needSave = false
	return nil
}

// Close 立即落盘一次（如需要）并停止防抖定时器；幂等。
func (e *Engine) Close() error {
	e.closeOnce.Do(func() {
		close(e.stopCh)
		<-e.doneCh // 等 saveLoop 退出，避免 goroutine 泄漏
		e.mu.Lock()
		_ = e.saveLocked()
		e.mu.Unlock()
	})
	return nil
}

// ---------- audit 订阅 ----------

// OnOp 实现 audit.Subscriber：把一次审计操作折算成宠物经验。
//
// 流程（见 docs/PET-FEATURE-DESIGN.md §5）：
//  1. 未解锁 → 立即返回（no-op 快门，零记录）；
//  2. 会话跟踪（ssh.shell.start 记录开始、ssh.shell.end 结算时长奖励）；
//  3. 查 ExpRules 白名单，0 分直接返回；
//  4. 冷却表去重（同 op+target 在冷却窗口内只计首次）；
//  5. 每日上限 / 单 op 每日频率上限；
//  6. 计分：经验、统计、流水、升级循环、进化阶段重算，最后触发防抖落盘。
func (e *Engine) OnOp(ev audit.OpEvent) {
	if !e.enabled() {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onOpLocked(ev)
}

// onOpLocked OnOp 的持锁实现（调用方必须已持锁）。
func (e *Engine) onOpLocked(ev audit.OpEvent) {
	now := time.Now()
	op := ev.Op
	target := targetFromFields(ev.Fields)

	// 会话跟踪（先于经验映射：ssh.shell.end 本身 0 分，但要结算时长）
	switch op {
	case "ssh.shell.start":
		e.sessions[target] = now
	case "ssh.shell.end":
		e.settleSession(target, now)
	}
	e.pruneSessionsLocked(now)

	exp, ok := e.rules.ExpRules[op]
	if !ok || exp <= 0 {
		return
	}
	e.award(op, target, exp, now, false, false)
}

// award 计分管线（v2 防刷三层，见 docs/PET-SKINS-V2-DESIGN.md §6）：
//
//  1. 频次异常：同 op+target 距上次计分 < 2s → 丢弃 + 重置该 key 冷却（惩罚）；
//  2. 冷却（含动态加倍：单 op 当日用量 ≥60% 上限 → 窗口×2；≥85% → ×4）；
//  3. 每日上限 / 单 op 每日频率上限；
//  4. 计分：经验、统计、流水、升级循环、进化阶段重算，最后触发防抖落盘。
//
// bypassCooldown / bypassOpMax 供合成 op（会话时长）跳过对应关卡；
// 频次异常检测不 bypass（合成 op 间隔天然 > 2s，不会被误伤）。
func (e *Engine) award(op, target string, exp int64, now time.Time, bypassCooldown, bypassOpMax bool) {
	key := op + "|" + target

	// 层 1：频次异常（脚本行为）丢弃 + 惩罚
	if lt, ok := e.lastAward[key]; ok && now.Sub(lt) < rapidRepeatWindow {
		e.cooldowns[key] = now // 重置冷却：惩罚窗口从现在重新起算
		return
	}

	// 跨天重置（先于上限与动态冷却：dailyOpCounts 必须是当日数据）
	e.checkDayLocked(now)

	// 层 2：冷却（合成 op 绕过）
	if !bypassCooldown && e.rules.CooldownMinutes > 0 {
		window := time.Duration(e.rules.CooldownMinutes) * time.Minute
		// 动态冷却：越接近单 op 每日上限，窗口越长
		if max, ok := e.rules.OpDailyMax[op]; ok && max > 0 {
			used := e.dailyOpCounts[op]
			switch {
			case used >= max*85/100:
				window *= 4
			case used >= max*60/100:
				window *= 2
			}
		}
		if last, ok := e.cooldowns[key]; ok && now.Sub(last) < window {
			return
		}
		e.cooldowns[key] = now
		e.pruneCooldownsLocked(now)
	}

	// 层 3：每日上限
	if e.todayEarned >= e.rules.DailyCap {
		return
	}
	// 单 op 每日频率上限（合成 op 绕过）
	if !bypassOpMax {
		if max, ok := e.rules.OpDailyMax[op]; ok && e.dailyOpCounts[op] >= max {
			return
		}
	}

	// 层 4：计分
	e.lastAward[key] = now
	e.todayEarned += exp
	e.dailyOpCounts[op]++
	e.state.TotalEarned += exp
	e.state.Dirty = true
	e.bumpStat(op, exp)
	e.appendLedger(op, now, exp)

	// 升级循环：状态内经验超过阈值就扣掉升一级（曲线无上限，等级无上限）
	e.state.Exp += exp
	next := NextExp(e.state.Level)
	for e.state.Exp >= next {
		e.state.Exp -= next
		e.state.Level++
		next = NextExp(e.state.Level)
	}
	e.state.Stage = stageForLevel(e.rules.StageLevels, e.state.Level)

	e.markDirtyLocked()
}

// settleSession 结算 ssh.shell.end 的会话时长奖励。
// 合成 op "ssh.session.time"：每满 SessionExpMinutes 分钟 +1，单会话封顶 +8；
// 会话时长不足 3min 不结算（防秒开秒关刷分）；
// 走同一条计分管线，但绕过冷却与单 op 频率上限。
func (e *Engine) settleSession(target string, now time.Time) {
	if e.rules.SessionExpMinutes <= 0 {
		return
	}
	start, ok := e.sessions[target]
	delete(e.sessions, target)
	if !ok {
		return
	}
	dur := now.Sub(start)
	if dur < 0 {
		// 时钟回拨：防御性丢弃
		return
	}
	if dur < sessionMinDuration {
		// 真实性门槛：短会话不计时长奖励
		return
	}
	award := int64(dur.Minutes()) / int64(e.rules.SessionExpMinutes)
	if award > sessionAwardCap {
		award = sessionAwardCap
	}
	if award <= 0 {
		return
	}
	e.award("ssh.session.time", target, award, now, true, true)
}

// checkDayLocked 跨天检查：日期变化时重置当日累计（调用方必须已持锁）。
func (e *Engine) checkDayLocked(now time.Time) {
	today := now.Format("2006-01-02")
	if e.todayKey != today {
		e.todayKey = today
		e.todayEarned = 0
		e.dailyOpCounts = make(map[string]int64)
	}
}

// pruneCooldownsLocked 偶尔清理冷却表：只清理超过 2× 冷却窗口的条目。
// 触发条件：表过大（防御恶意 op+target 组合撑爆内存）。
func (e *Engine) pruneCooldownsLocked(now time.Time) {
	if len(e.cooldowns) <= 1024 {
		return
	}
	cutoff := now.Add(-2 * time.Duration(e.rules.CooldownMinutes) * time.Minute)
	for k, v := range e.cooldowns {
		if v.Before(cutoff) {
			delete(e.cooldowns, k)
		}
	}
}

// pruneSessionsLocked 偶尔清理孤儿会话（>24h 未结束），防止表无限增长。
func (e *Engine) pruneSessionsLocked(now time.Time) {
	if len(e.sessions) <= 64 {
		return
	}
	cutoff := now.Add(-sessionStaleAfter)
	for k, v := range e.sessions {
		if v.Before(cutoff) {
			delete(e.sessions, k)
		}
	}
}

// ---------- 辅助 ----------

// NextExp 升到下一级所需经验（成长曲线：80 * level^1.2，等级无上限）。
// L1→2 需 80，L10→11 约 1,266，L50→51 约 10,456…
// 曲线设计（见 docs/PET-SKINS-V2-DESIGN.md §5）：满额天 Lv8≈4 天 / Lv16≈13 天 /
// Lv50≈100 天；普通用户（日均 ~80 分）Lv8 约 2 周，首个进化目标可达。
func NextExp(level int) int64 {
	if level < 1 {
		level = 1
	}
	return int64(math.Round(80 * math.Pow(float64(level), 1.2)))
}

// stageForLevel 按等级区间查进化阶段；未匹配默认 "egg"。
// 阶段名按字典序迭代，保证区间重叠时结果确定。
func stageForLevel(stageLevels map[string][2]int, level int) string {
	names := make([]string, 0, len(stageLevels))
	for name := range stageLevels {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rng := stageLevels[name]
		if level >= rng[0] && level <= rng[1] {
			return name
		}
	}
	return "egg"
}

// targetFromFields 从 audit 字段里提取目标：优先 "system"，其次 "host"，否则 ""。
// Fields 是 key/value 交替切片；非字符串 key 用 fmt.Sprint 兜底。
func targetFromFields(fields []any) string {
	host := ""
	for i := 0; i+1 < len(fields); i += 2 {
		k, ok := fields[i].(string)
		if !ok {
			k = fmt.Sprint(fields[i])
		}
		switch k {
		case "system":
			return fmt.Sprint(fields[i+1])
		case "host":
			host = fmt.Sprint(fields[i+1])
		}
	}
	return host
}
