// Package reminder 提供"便笺提醒"功能：定时提醒 + 系统通知弹窗。
//
// 设计原则：
//   - 事件驱动调度（time.AfterFunc），非轮询。CPU 占用 ≈ 0。
//   - 四种 reminder 类型：once（单次）/ weekly（周循环）/ monthly（月循环）/ cron（cron 表达式）。
//   - 支持提前 N 分钟触发（lead_minutes，如"会前 10 分钟"）。
//   - 支持触发动作扩展（action）：弹窗 popup / 打开网址 url / 执行本地命令 command。
//   - JSON 持久化，存 data/reminders.json。
//   - 触发后通过 Manager.OnFire 回调给上层（popup 包）。
//   - 触发一次后单次提醒自动 disabled（保留记录方便回顾）。
//
// 数据流：
//
//	HTTP API → Manager.Add/Update/Delete → Store.Save → Scheduler.Rebuild
//	                                                      ↓
//	                                            time.AfterFunc(NextFire)
//	                                                      ↓
//	                                              Manager.OnFire
//	                                                      ↓
//	                                    按 Action.Kind 分发（popup/url/command）
package reminder

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"kairo/internal/cronx"
)

// Type 提醒类型
type Type string

const (
	TypeOnce    Type = "once"    // 单次：指定日期时间
	TypeWeekly  Type = "weekly"  // 周循环：周几 + 时间
	TypeMonthly Type = "monthly" // 月循环：每月第几天 + 时间
	TypeCron    Type = "cron"    // cron 表达式：5/6 段或 @descriptor
)

// WeekdayNames 中文星期名（周一为一周的第一天，跟中国习惯一致）
var WeekdayNames = []string{"周一", "周二", "周三", "周四", "周五", "周六", "周日"}

// ActionKind 触发动作类型。
type ActionKind string

const (
	ActionPopup   ActionKind = "popup"   // 弹窗提醒（默认）
	ActionURL     ActionKind = "url"     // 打开网址
	ActionCommand ActionKind = "command" // 执行本地命令/脚本
)

// Action 触发动作。Kind 为空视为 popup。
type Action struct {
	Kind    ActionKind `json:"kind"`               // popup | url | command
	URL     string     `json:"url,omitempty"`      // kind=url：http/https 地址
	Command string     `json:"command,omitempty"`  // kind=command：可执行文件路径
	Args    []string   `json:"args,omitempty"`     // kind=command：参数
	WorkDir string     `json:"work_dir,omitempty"` // kind=command：工作目录
}

// Reminder 一条提醒。
//
// 所有时间相关字段都基于服务器本地时区（time.Local）。
// type=once: At 必填（精确到分钟），其余时间字段必须为零值。
// type=weekly: Weekdays (1-7, 1=周一) + Time (HH:MM) 必填。
// type=monthly: DayOfMonth (1-31) + Time (HH:MM) 必填。
// type=cron: Cron 必填（5/6 段 cron 或 @descriptor）。
// lead_minutes: 提前触发分钟数（0 = 到点触发；fire 时刻 = 调度槽 − lead）。
type Reminder struct {
	ID          string `json:"id"`                      // UUID
	Type        Type   `json:"type"`                    // once | weekly | monthly | cron
	Enabled     bool   `json:"enabled"`                 // 是否启用
	Content     string `json:"content"`                 // 提醒内容（≤ 200 字）
	CreatedAt   string `json:"created_at"`              // RFC3339
	UpdatedAt   string `json:"updated_at"`              // RFC3339
	LastFiredAt string `json:"last_fired_at,omitempty"` // RFC3339；用于"已触发"判断
	FiredCount  int    `json:"fired_count"`             // 累计触发次数

	// type=once: 必填，格式 "2006-01-02T15:04"
	At string `json:"at,omitempty"`

	// type=weekly: 必填，元素 ∈ [1,7]，1=周一，7=周日
	Weekdays []int `json:"weekdays,omitempty"`

	// type=weekly / type=monthly: 必填，格式 "HH:MM"
	Time string `json:"time,omitempty"`

	// type=monthly: 必填，∈ [1,31]
	DayOfMonth int `json:"day_of_month,omitempty"`

	// type=cron: 必填，5/6 段 cron 或 @descriptor（复用 internal/cronx）
	Cron string `json:"cron,omitempty"`

	// 提前触发分钟数，∈ [0,1440]。0 = 到点触发。
	LeadMinutes int `json:"lead_minutes,omitempty"`

	// 触发动作；nil 或 Kind 为空 = popup。
	Action *Action `json:"action,omitempty"`

	// SourceNoteID is an optional weak link to the note that created this
	// reminder. Reminder content is a snapshot; deleting or editing the note
	// never changes scheduling or delivery.
	SourceNoteID string `json:"source_note_id,omitempty"`
}

// leadDur 提前量（负值/0 一律视为 0）。
func (r *Reminder) leadDur() time.Duration {
	if r.LeadMinutes <= 0 {
		return 0
	}
	return time.Duration(r.LeadMinutes) * time.Minute
}

// Validate 校验字段合法性，返回首个错误。
func (r *Reminder) Validate() error {
	if r.LeadMinutes < 0 || r.LeadMinutes > 1440 {
		return fmt.Errorf("lead_minutes 必须在 0-1440（分钟）之间：%d", r.LeadMinutes)
	}
	if err := r.validateAction(); err != nil {
		return err
	}
	switch r.Type {
	case TypeOnce:
		if r.At == "" {
			return errors.New("单次提醒必须填写 at（日期时间）")
		}
		if _, err := time.ParseInLocation("2006-01-02T15:04", r.At, time.Local); err != nil {
			return fmt.Errorf("at 时间格式错误（应为 YYYY-MM-DDTHH:MM）：%w", err)
		}
	case TypeWeekly:
		if len(r.Weekdays) == 0 {
			return errors.New("周循环提醒必须至少勾选一天")
		}
		seen := map[int]bool{}
		for _, d := range r.Weekdays {
			if d < 1 || d > 7 {
				return fmt.Errorf("weekdays 元素越界（1-7，1=周一）：%d", d)
			}
			if seen[d] {
				return fmt.Errorf("weekdays 重复：%d", d)
			}
			seen[d] = true
		}
		if _, err := parseHHMM(r.Time); err != nil {
			return err
		}
	case TypeMonthly:
		if r.DayOfMonth < 1 || r.DayOfMonth > 31 {
			return fmt.Errorf("day_of_month 越界（1-31）：%d", r.DayOfMonth)
		}
		if _, err := parseHHMM(r.Time); err != nil {
			return err
		}
	case TypeCron:
		if strings.TrimSpace(r.Cron) == "" {
			return errors.New("Cron 提醒必须填写 cron 表达式")
		}
		if _, err := cronx.Parse(r.Cron); err != nil {
			return fmt.Errorf("cron 表达式无效：%w", err)
		}
	default:
		return fmt.Errorf("未知 type：%q（允许 once/weekly/monthly/cron）", r.Type)
	}
	content := strings.TrimSpace(r.Content)
	if content == "" {
		return errors.New("content 不能为空")
	}
	if len([]rune(content)) > 200 {
		return errors.New("content 不能超过 200 字")
	}
	return nil
}

// validateAction 校验触发动作。
func (r *Reminder) validateAction() error {
	if r.Action == nil {
		return nil
	}
	switch r.Action.Kind {
	case "", ActionPopup:
		return nil
	case ActionURL:
		u := strings.TrimSpace(r.Action.URL)
		if u == "" {
			return errors.New("url 动作必须填写网址")
		}
		if len(u) > 2048 {
			return errors.New("url 过长（≤ 2048 字符）")
		}
		parsed, err := url.Parse(u)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("url 只支持 http/https 绝对地址")
		}
		return nil
	case ActionCommand:
		cmd := strings.TrimSpace(r.Action.Command)
		if cmd == "" {
			return errors.New("command 动作必须填写可执行文件路径")
		}
		if len([]rune(cmd)) > 512 {
			return errors.New("command 过长（≤ 512 字符）")
		}
		if len([]rune(r.Action.WorkDir)) > 512 {
			return errors.New("work_dir 过长（≤ 512 字符）")
		}
		if len(r.Action.Args) > 64 {
			return errors.New("参数最多 64 个")
		}
		for i, a := range r.Action.Args {
			if len([]rune(a)) > 256 {
				return fmt.Errorf("第 %d 个参数过长（≤ 256 字符）", i+1)
			}
		}
		return nil
	default:
		return fmt.Errorf("未知动作类型：%q（允许 popup/url/command）", r.Action.Kind)
	}
}

// parseHHMM 解析 "HH:MM"，校验合法。
func parseHHMM(s string) (time.Time, error) {
	t, err := time.ParseInLocation("15:04", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("time 格式错误（应为 HH:MM）：%q", s)
	}
	return t, nil
}

// NextFire 计算下一次触发时间。
//
// 语义：fire 时刻 = 调度槽 − lead_minutes。lead=0 时即"到点触发"。
//
// - 已过 / 无效：返回零值 time.Time{}，调用方应跳过。
// - 单次：at − lead 已过则返回零值。
// - 周循环：今天还有今天，没就下一个勾选的周几（lead 导致今天的槽提前量已过则顺延）。
// - 月循环：本月已过则下月；月末越界（如 31 号在 2 月）回退到当月最后一天。
// - cron：cronx.Spec.Next 找 > now+lead 的下一个命中槽，再减 lead。
//
// 注意：返回的是"理想触发时间"。调度器如果在电脑休眠/关机期间错过，
// 唤醒后会按当前时间重新算下一次，不补触发（避免凌晨 3 点电脑开机被 8 个
// 积压提醒轰炸）。
func (r *Reminder) NextFire(now time.Time) time.Time {
	if !r.Enabled {
		return time.Time{}
	}
	lead := r.leadDur()
	switch r.Type {
	case TypeOnce:
		t, err := time.ParseInLocation("2006-01-02T15:04", r.At, time.Local)
		if err != nil || !t.After(now.Add(lead)) {
			return time.Time{}
		}
		return t.Add(-lead)
	case TypeWeekly:
		hh, mm, err := parseHHMMParts(r.Time)
		if err != nil {
			return time.Time{}
		}
		// 在 weekdays 里查今天或之后最近的星期几。今天已过则跳到下周。
		today := int(now.Weekday())
		if today == 0 {
			today = 7 // 周日 → 7
		}
		// 优先：今天的 HH:MM 还没到（考虑 lead 提前量）
		if contains(r.Weekdays, today) {
			candidate := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, time.Local)
			if candidate.After(now.Add(lead)) {
				return candidate.Add(-lead)
			}
		}
		// 否则找未来 14 天内最近的勾选日。用 14 天而非 7 天：lead（≤24h）可能让
		// 第一个候选的 fire 时刻已过，需要顺延到下一周的同一勾选日。
		for offset := 1; offset <= 14; offset++ {
			d := today + offset
			for d > 7 {
				d -= 7
			}
			if contains(r.Weekdays, d) {
				candidate := time.Date(now.Year(), now.Month(), now.Day()+offset, hh, mm, 0, 0, time.Local)
				if candidate.After(now.Add(lead)) {
					return candidate.Add(-lead)
				}
			}
		}
		return time.Time{}
	case TypeMonthly:
		hh, mm, err := parseHHMMParts(r.Time)
		if err != nil {
			return time.Time{}
		}
		// 本月
		candidate := monthCandidate(now.Year(), now.Month(), r.DayOfMonth, hh, mm)
		if candidate.After(now.Add(lead)) {
			return candidate.Add(-lead)
		}
		// 下月（距今 ≥ 28 天，lead ≤ 24h，fire 时刻必在未来）
		y, m := now.Year(), now.Month()+1
		if m > 12 {
			y++
			m = 1
		}
		return monthCandidate(y, m, r.DayOfMonth, hh, mm).Add(-lead)
	case TypeCron:
		spec, err := cronx.Parse(r.Cron)
		if err != nil {
			return time.Time{}
		}
		step := time.Minute
		if spec.HasSeconds() {
			step = time.Second
		}
		// 严格未来：slot > now + lead ⟺ fire 时刻 > now。
		// from 取 trunc(now+lead) 的下一个刻度，Next 是含 from 的。
		m := spec.Next(now.Add(lead).Truncate(step).Add(step))
		if m.IsZero() {
			return time.Time{}
		}
		return m.Add(-lead)
	}
	return time.Time{}
}

// DueAt 返回 <= now 的最近一次触发时刻（即"刚刚到达"的那个 fire 槽）。
//
// 与 NextFire（只返回严格未来）互补，专供 fire() 判断"哪些提醒该触发"用。
// 若不存在这样的槽（如 once 还没到点、weekly 本周尚未到勾选日），返回零值。
//
// 注意：对周/月循环，若当前已过本周/本月槽，返回的是当前周期的槽；
// 若当前周期槽尚未到，返回上一个周期的槽（可能距今很久，由调用方用容差窗口过滤）。
//
// lead 语义与 NextFire 一致：fire 时刻 = 调度槽 − lead。lead=0 时行为与旧版完全一致。
func (r *Reminder) DueAt(now time.Time) time.Time {
	if !r.Enabled {
		return time.Time{}
	}
	lead := r.leadDur()
	switch r.Type {
	case TypeOnce:
		t, err := time.ParseInLocation("2006-01-02T15:04", r.At, time.Local)
		if err != nil || t.After(now.Add(lead)) {
			return time.Time{}
		}
		return t.Add(-lead)
	case TypeWeekly:
		hh, mm, err := parseHHMMParts(r.Time)
		if err != nil {
			return time.Time{}
		}
		today := int(now.Weekday())
		if today == 0 {
			today = 7 // 周日 → 7
		}
		// 今天的槽已到（槽 ≤ now + lead ⟺ fire 时刻 ≤ now）→ 直接返回
		if contains(r.Weekdays, today) {
			candidate := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, time.Local)
			if !candidate.After(now.Add(lead)) {
				return candidate.Add(-lead)
			}
		}
		// 否则找过去 7 天内最近的勾选日（过去的槽 − lead 必 ≤ now）
		for offset := 1; offset <= 7; offset++ {
			d := today - offset
			if d < 1 {
				d += 7
			}
			if contains(r.Weekdays, d) {
				candidate := time.Date(now.Year(), now.Month(), now.Day()-offset, hh, mm, 0, 0, time.Local)
				return candidate.Add(-lead)
			}
		}
		return time.Time{}
	case TypeMonthly:
		hh, mm, err := parseHHMMParts(r.Time)
		if err != nil {
			return time.Time{}
		}
		// 本月槽已到 → 返回
		candidate := monthCandidate(now.Year(), now.Month(), r.DayOfMonth, hh, mm)
		if !candidate.After(now.Add(lead)) {
			return candidate.Add(-lead)
		}
		// 否则上个月
		y, m := now.Year(), now.Month()-1
		if m < 1 {
			y--
			m = 12
		}
		return monthCandidate(y, m, r.DayOfMonth, hh, mm).Add(-lead)
	case TypeCron:
		spec, err := cronx.Parse(r.Cron)
		if err != nil {
			return time.Time{}
		}
		step := time.Minute
		if spec.HasSeconds() {
			step = time.Second
		}
		// 从 trunc(now+lead) 起向后扫描（槽 ≤ now+lead ⟺ fire 时刻 ≤ now）。
		// fire 时刻须 ≥ now − 容差窗口，即槽 ≥ now + lead − 窗口，所以只需回看
		// 3 分钟（fire() 容差窗口 2min + 1min 抖动余量），与 lead 大小无关。
		// 找不到说明最近一次 fire 已超出容差窗口，返回零值（不补触发）；
		// 找到但较陈旧时由 fire() 的窗口 + LastFiredAt 防重逻辑过滤。
		t := now.Add(lead).Truncate(step)
		const maxBack = 3 * time.Minute
		for i := time.Duration(0); i <= maxBack; i += step {
			if spec.Match(t) {
				return t.Add(-lead)
			}
			t = t.Add(-step)
		}
		return time.Time{}
	}
	return time.Time{}
}

// monthCandidate 构造"指定年月的第 N 天 HH:MM"，N 越界则回退到月末。
func monthCandidate(y int, m time.Month, day, hh, mm int) time.Time {
	lastDay := daysInMonth(y, m)
	if day > lastDay {
		day = lastDay
	}
	return time.Date(y, m, day, hh, mm, 0, 0, time.Local)
}

func daysInMonth(y int, m time.Month) int {
	// 下个月第 0 天 = 本月最后一天
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.Local).Day()
}

func parseHHMMParts(s string) (hh, mm int, err error) {
	t, err := time.ParseInLocation("15:04", s, time.Local)
	if err != nil {
		return 0, 0, err
	}
	return t.Hour(), t.Minute(), nil
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// HumanSchedule 返回给人看的"调度描述"。
//
// 例如：
//   - once: "2026-07-20 15:00"
//   - weekly: "工作日 17:00" / "周一、周五 08:30"
//   - monthly: "每月 20 号 09:00"
//   - cron: "Cron: 0 9 * * 1-5"
//
// lead_minutes > 0 时统一追加 " · 提前 N 分钟"。
func (r *Reminder) HumanSchedule() string {
	var s string
	switch r.Type {
	case TypeOnce:
		if t, err := time.ParseInLocation("2006-01-02T15:04", r.At, time.Local); err == nil {
			s = t.Format("2006-01-02 15:04")
		} else {
			s = r.At
		}
	case TypeWeekly:
		s = weekdaysToNames(r.Weekdays) + " " + r.Time
	case TypeMonthly:
		s = fmt.Sprintf("每月 %d 号 %s", r.DayOfMonth, r.Time)
	case TypeCron:
		s = "Cron: " + strings.TrimSpace(r.Cron)
	default:
		s = string(r.Type)
	}
	if r.LeadMinutes > 0 {
		s = fmt.Sprintf("%s · 提前 %d 分钟", s, r.LeadMinutes)
	}
	return s
}

// weekdaysToNames 把 [1,2,3,4,5] 转 "工作日" 或 "周一、周二"。
func weekdaysToNames(days []int) string {
	if isWeekdays(days) {
		return "工作日"
	}
	if isWeekend(days) {
		return "周末"
	}
	names := make([]string, 0, len(days))
	for _, d := range days {
		if d >= 1 && d <= 7 {
			names = append(names, WeekdayNames[d-1])
		}
	}
	return strings.Join(names, "、")
}

func isWeekdays(d []int) bool {
	if len(d) != 5 {
		return false
	}
	want := map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true}
	for _, x := range d {
		if !want[x] {
			return false
		}
	}
	return true
}

func isWeekend(d []int) bool {
	if len(d) != 2 {
		return false
	}
	return contains(d, 6) && contains(d, 7)
}

// IsExpired 单次提醒触发过 / 已过期。
func (r *Reminder) IsExpired(now time.Time) bool {
	if r.Type != TypeOnce {
		return false
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", r.At, time.Local)
	if err != nil {
		return true
	}
	return !t.After(now)
}
