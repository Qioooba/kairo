package pet

import "time"

// ---------- 未同步流水（Ledger）----------
//
// Ledger 挂在 State 里，只含 op/ts/exp（不含主机名/IP/路径，保护隐私）。
// 它记录"上次服务器同步之后"新赚的经验，供 sync 流程上传（见 sync.go）。
// 环形上限 rules.MaxLedger：满时丢最旧、留最新。

// appendLedger 追加一条流水并裁剪到 MaxLedger（调用方必须已持锁）。
func (e *Engine) appendLedger(op string, ts time.Time, exp int64) {
	if e.rules.MaxLedger <= 0 {
		return
	}
	entry := LedgerEntry{
		Op:  op,
		Ts:  ts.Format(time.RFC3339), // RFC3339 local（带时区偏移）
		Exp: exp,
	}
	e.state.Ledger = append(e.state.Ledger, entry)
	if n := len(e.state.Ledger); n > e.rules.MaxLedger {
		// 保留最新 MaxLedger 条（丢弃最旧），避免反复 append 导致底层数组膨胀
		e.state.Ledger = append([]LedgerEntry(nil), e.state.Ledger[n-e.rules.MaxLedger:]...)
	}
}

// TakeLedger 返回未同步流水的深拷贝（不清空原表，供 sync 读取）。
func (e *Engine) TakeLedger() []LedgerEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]LedgerEntry, len(e.state.Ledger))
	copy(out, e.state.Ledger)
	return out
}

// MarkSynced 标记一次服务器同步完成：回写认可分与同步时间、
// 清空已上传流水、Dirty=false，并触发防抖落盘。
//
// 设计（见 docs/PET-FEATURE-DESIGN.md §2）：sync 成功后调用；
// 同步失败则不应调用（保留 dirty，下次切榜重试）。
func (e *Engine) MarkSynced(boardExp int64, syncedAt string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state.BoardExp = boardExp
	e.state.LastSync = syncedAt
	e.state.Ledger = nil
	e.state.Dirty = false
	e.markDirtyLocked()
}
