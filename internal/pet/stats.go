package pet

import "time"

// ---------- 本地统计（Stats）----------
//
// 三层聚合：Total（全历史）、Daily（key "2006-01-02"）、Monthly（key "2006-01"）。
// key 的字典序即时间序（固定宽度格式），因此清理旧数据只需字符串比较，
// 无需解析成 time.Time。
//
// 注意：Stats 只用于本地展示；排行榜认可分一律以服务器重算为准。

// bumpStat 给 op 的三层统计各加一次计分（Count+1、Exp+=exp），
// 并顺带清理超出保留窗口的旧条目（调用方必须已持锁）。
func (e *Engine) bumpStat(op string, exp int64) {
	now := time.Now()
	day := now.Format("2006-01-02")
	month := now.Format("2006-01")

	st := e.state.Stats

	// Total
	if st.Total == nil {
		st.Total = make(map[string]OpStat)
	}
	t := st.Total[op]
	t.Count++
	t.Exp += exp
	st.Total[op] = t

	// Daily
	if st.Daily == nil {
		st.Daily = make(map[string]map[string]OpStat)
	}
	daily := st.Daily[day]
	if daily == nil {
		daily = make(map[string]OpStat)
		st.Daily[day] = daily
	}
	d := daily[op]
	d.Count++
	d.Exp += exp
	daily[op] = d

	// Monthly
	if st.Monthly == nil {
		st.Monthly = make(map[string]map[string]OpStat)
	}
	monthly := st.Monthly[month]
	if monthly == nil {
		monthly = make(map[string]OpStat)
		st.Monthly[month] = monthly
	}
	m := monthly[op]
	m.Count++
	m.Exp += exp
	monthly[op] = m

	// 清理（低频计分场景下逐次做也足够便宜，且无需额外的日切钩子）
	e.pruneStatsLocked(now)
}

// pruneStatsLocked 删除超出保留窗口的 Daily / Monthly 条目（调用方必须已持锁）。
//
// 比较规则：统计 key 用固定宽度日期格式（"2006-01-02" / "2006-01"），
// 字典序 == 时间序，直接 `key < cutoff` 即可正确判断过期。
func (e *Engine) pruneStatsLocked(now time.Time) {
	st := e.state.Stats

	if e.rules.StatsKeepDays > 0 {
		cutoff := now.AddDate(0, 0, -e.rules.StatsKeepDays).Format("2006-01-02")
		for k := range st.Daily {
			if k < cutoff {
				delete(st.Daily, k)
			}
		}
	}
	if e.rules.StatsKeepMonths > 0 {
		cutoff := now.AddDate(0, -e.rules.StatsKeepMonths, 0).Format("2006-01")
		for k := range st.Monthly {
			if k < cutoff {
				delete(st.Monthly, k)
			}
		}
	}
}
