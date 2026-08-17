// Package cronx 提供 5/6 段 cron 表达式的解析与时间匹配。
//
// 语义（与历史行为保持一致，勿改）：
//   - day-of-month 与 day-of-week 是 AND 关系（非标准 cron 的 OR 规则）；
//   - 支持 @yearly / @annually / @monthly / @weekly / @daily / @midnight / @hourly；
//   - 不支持 @every（固定间隔，由调用方自行处理）；
//   - 6 段时首段为秒；5 段时按 [分 时 日 月 周] 处理。
//
// Next 用字段递进跳跃算法（月→日→时→分→秒），稀疏 cron（如每年 1 月 1 日）
// 也不会退化成逐分钟暴力遍历（BE-015）。
package cronx

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Spec 一条解析后的 cron 表达式。
type Spec struct {
	fields     []string // 6 段（5 段输入时已补前置 "0"）
	hasSeconds bool
	sets       []map[int]bool // 6 组：秒/分/时/日/月/周
}

// Parse 解析 cron 表达式。合法输入：5 段 / 6 段 / @descriptor。
func Parse(expr string) (*Spec, error) {
	input := strings.TrimSpace(expr)
	if input == "" {
		return nil, errors.New("表达式不能为空")
	}
	if strings.HasPrefix(strings.ToLower(input), "@every") {
		return nil, errors.New("@every 固定间隔不受支持，请用 5/6 段 cron 或 @hourly 等描述符")
	}
	fields := strings.Fields(ExpandDescriptor(input))
	if len(fields) != 5 && len(fields) != 6 {
		return nil, errors.New("Cron 需要 5 段或 6 段，或使用 @hourly / @daily / @weekly / @monthly / @yearly")
	}
	hasSeconds := len(fields) == 6
	if !hasSeconds {
		fields = append([]string{"0"}, fields...)
	}
	sets, err := ParseFields(fields)
	if err != nil {
		return nil, err
	}
	return &Spec{fields: fields, hasSeconds: hasSeconds, sets: sets}, nil
}

// ExpandDescriptor 把 @descriptor 展开成 5 段 cron 文本；非描述符原样返回。
func ExpandDescriptor(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "@yearly", "@annually":
		return "0 0 1 1 *"
	case "@monthly":
		return "0 0 1 * *"
	case "@weekly":
		return "0 0 * * 0"
	case "@daily", "@midnight":
		return "0 0 * * *"
	case "@hourly":
		return "0 * * * *"
	default:
		return input
	}
}

// ParseFields 解析 6 段字段（5 段调用方需先补前置 "0" 秒段）。
// 周字段的 7（周日）自动归一化为 0。
func ParseFields(fields []string) ([]map[int]bool, error) {
	ranges := [][2]int{{0, 59}, {0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	sets := make([]map[int]bool, len(fields))
	for i, f := range fields {
		set, err := parseCronField(f, ranges[i][0], ranges[i][1])
		if err != nil {
			return nil, fmt.Errorf("第 %d 段解析失败: %w", i+1, err)
		}
		if i == 5 && set[7] {
			set[0] = true
			delete(set, 7)
		}
		sets[i] = set
	}
	return sets, nil
}

// parseCronField 解析单段（支持 * ? a-b a/n n1,n2 及组合）。
func parseCronField(expr string, min, max int) (map[int]bool, error) {
	out := make(map[int]bool)
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("空字段")
		}
		step := 1
		base := part
		if strings.Contains(part, "/") {
			pair := strings.SplitN(part, "/", 2)
			base = pair[0]
			n, err := strconv.Atoi(pair[1])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("非法步长 %q", pair[1])
			}
			step = n
		}
		start, end := min, max
		switch {
		case base == "*" || base == "?":
		case strings.Contains(base, "-"):
			pair := strings.SplitN(base, "-", 2)
			a, errA := strconv.Atoi(pair[0])
			b, errB := strconv.Atoi(pair[1])
			if errA != nil || errB != nil || a > b {
				return nil, fmt.Errorf("非法范围 %q", base)
			}
			start, end = a, b
		default:
			n, err := strconv.Atoi(base)
			if err != nil {
				return nil, fmt.Errorf("非法值 %q", base)
			}
			start, end = n, n
		}
		if start < min || end > max {
			return nil, fmt.Errorf("值超出范围 %d-%d", min, max)
		}
		for i := start; i <= end; i += step {
			out[i] = true
		}
	}
	return out, nil
}

// Fields 返回 6 段原始字段文本（5 段输入时首段补 "0"），供 field_desc 展示。
func (s *Spec) Fields() []string { return append([]string(nil), s.fields...) }

// HasSeconds 是否为 6 段（含秒）表达式。
func (s *Spec) HasSeconds() bool { return s.hasSeconds }

// Match 判断 t 是否命中。day-of-month 与 day-of-week 为 AND 语义（与既有行为一致）。
func (s *Spec) Match(t time.Time) bool {
	return s.sets[0][t.Second()] &&
		s.sets[1][t.Minute()] &&
		s.sets[2][t.Hour()] &&
		s.sets[3][t.Day()] &&
		s.sets[4][int(t.Month())] &&
		s.sets[5][int(t.Weekday())]
}

// Next 返回 >= from 的下一个命中时刻（含 from 本身）。
// 跳跃算法：月→日→时→分→秒逐级递进，任一字段不匹配即跳到该字段下一个可能边界。
// 100000 次跳跃仍无匹配时返回零值（正常表达式不会发生）。
func (s *Spec) Next(from time.Time) time.Time {
	loc := from.Location()
	t := from
	for i := 0; i < 100000; i++ {
		if !s.sets[4][int(t.Month())] {
			// 月份不匹配：跳到下个月 1 号 00:00:00（Go 自动归一化 12→次年 1 月）。
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.dayMatches(t) {
			// 日期不匹配：跳到下一天 00:00:00。
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
			continue
		}
		if !s.sets[2][t.Hour()] {
			// 小时不匹配：跳到下一小时 00 分 00 秒。
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc).Add(time.Hour)
			continue
		}
		if !s.sets[1][t.Minute()] {
			// 分钟不匹配：跳到下一分钟 00 秒。
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc).Add(time.Minute)
			continue
		}
		if s.hasSeconds && !s.sets[0][t.Second()] {
			t = t.Add(time.Second)
			continue
		}
		return t
	}
	return time.Time{}
}

// dayMatches 判断 t 的日期部分是否匹配 day-of-month 与 day-of-week。
// 与 Match 保持一致的 AND 语义（既有行为，不改 cron 标准 OR 规则）。
func (s *Spec) dayMatches(t time.Time) bool {
	return s.sets[3][t.Day()] && s.sets[5][int(t.Weekday())]
}
