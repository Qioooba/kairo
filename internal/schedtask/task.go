// Package schedtask 提供"定时任务"功能：按 cron 调度在本地执行 shell 命令。
//
// 典型场景：SVN 定时更新、Git 定时拉取 / 定时提交、本地脚本自动运行。
//
// 设计原则（与 reminder 包同构）：
//   - 事件驱动调度（time.AfterFunc），非轮询。CPU 占用 ≈ 0。
//   - 调度表达式复用 cronx 包（5 段 / 6 段 / @描述符），统一技术栈。
//   - JSON 持久化：任务定义存 data/sched_tasks.json，运行历史存 data/sched_task_runs.json。
//   - 同一任务防重叠：上一次还在跑则跳过本次（记 skipped），避免 git pull 叠加。
//   - 错过不补跑：超过容差窗口（2 分钟）的槽直接放弃，避免休眠唤醒后任务风暴。
//
// 数据流：
//
//	HTTP API → Manager.Add/Update/Delete → Store.Save → Rebuild
//	                                                      ↓
//	                                            time.AfterFunc(NextRun)
//	                                                      ↓
//	                                              runner.Run（goroutine）
//	                                                      ↓
//	                                              运行历史 + 状态更新
package schedtask

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"kairo/internal/cronx"
)

// 运行状态取值。
const (
	StatusSuccess  = "success"  // exit code = 0
	StatusFailed   = "failed"   // exit code != 0 或启动失败
	StatusTimeout  = "timeout"  // 超时被杀
	StatusCanceled = "canceled" // 程序退出等生命周期取消
	StatusSkipped  = "skipped"  // 上一次还在跑，本次跳过
	StatusRunning  = "running"  // 正在运行（仅瞬时状态，不持久化为最终态）
)

const (
	// DefaultTimeoutSec 默认单任务超时（5 分钟）。
	DefaultTimeoutSec = 300
	// MaxTimeoutSec 超时上限（1 小时），防止用户配出"永远跑不完"的任务。
	MaxTimeoutSec = 3600
	// maxCommandLen 命令长度上限，防误粘贴超大脚本撑爆 JSON。
	maxCommandLen = 4000
	// maxNameLen 名称长度上限。
	maxNameLen = 60
)

// Task 一条定时任务。
//
// 所有时间字段为 RFC3339（服务器本地时区）。
// Cron 是唯一的调度表达方式：UI 上的"每 5 分钟 / 每天 9 点"等预设最终都落地为 cron。
type Task struct {
	ID         string `json:"id"`
	Name       string `json:"name"`                  // 任务名（≤ 60 字）
	Enabled    bool   `json:"enabled"`               // 是否启用
	Cron       string `json:"cron"`                  // 5/6 段 cron 或 @描述符
	Command    string `json:"command"`               // shell 命令（可多行，≤ 4000 字）
	WorkDir    string `json:"work_dir,omitempty"`    // 工作目录；空 = 工具运行目录
	TimeoutSec int    `json:"timeout_sec,omitempty"` // 单任务超时秒数；0 = 默认 300
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`

	// ---- 运行态（由 manager 维护，PUT 时客户端传入会被忽略）----
	LastRunAt      string `json:"last_run_at,omitempty"`      // 上次开始运行时间
	LastStatus     string `json:"last_status,omitempty"`      // success/failed/timeout/canceled/skipped/running
	LastDurationMs int64  `json:"last_duration_ms,omitempty"` // 上次耗时（毫秒）
	LastError      string `json:"last_error,omitempty"`       // 上次失败原因摘要（stderr 尾部 / 启动错误）
	RunCount       int    `json:"run_count"`                  // 累计运行次数（含失败）
}

// Validate 校验字段合法性，返回首个错误。
func (t *Task) Validate() error {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return errors.New("任务名称不能为空")
	}
	if len([]rune(name)) > maxNameLen {
		return fmt.Errorf("任务名称不能超过 %d 字", maxNameLen)
	}
	cmd := strings.TrimSpace(t.Command)
	if cmd == "" {
		return errors.New("执行命令不能为空")
	}
	if len([]rune(cmd)) > maxCommandLen {
		return fmt.Errorf("执行命令不能超过 %d 字", maxCommandLen)
	}
	if strings.TrimSpace(t.Cron) == "" {
		return errors.New("调度表达式（cron）不能为空")
	}
	if _, err := cronx.Parse(t.Cron); err != nil {
		return fmt.Errorf("cron 表达式不合法：%w", err)
	}
	if t.TimeoutSec < 0 || t.TimeoutSec > MaxTimeoutSec {
		return fmt.Errorf("超时时间须在 0-%d 秒之间（0 = 默认 %d 秒）", MaxTimeoutSec, DefaultTimeoutSec)
	}
	if dir := strings.TrimSpace(t.WorkDir); dir != "" {
		st, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("工作目录不可访问：%s", dir)
		}
		if !st.IsDir() {
			return fmt.Errorf("工作目录不是文件夹：%s", dir)
		}
	}
	return nil
}

// Timeout 返回生效的超时时长。
func (t *Task) Timeout() time.Duration {
	if t.TimeoutSec <= 0 {
		return time.Duration(DefaultTimeoutSec) * time.Second
	}
	return time.Duration(t.TimeoutSec) * time.Second
}

// NextRun 计算 > now 的下一次运行时间。禁用 / 表达式异常返回零值。
//
// 关键：base 必须按调度粒度向上取整（5 段 → 下一分钟整；6 段 → 下一秒整）。
// 因为 cronx.Spec.Next 对 5 段表达式跳过秒字段检查，"匹配分钟内的任意秒"
// 都算命中 —— 如果直接用 now+1s，触发后重算会得到"当前分钟的下一秒"，
// timer 以 ~1s 间隔连发，直到这一分钟走完（实测每分钟连发 60 次）。
// 量化到下一分钟后，已触发的槽不可能被再次选中。
func (t *Task) NextRun(now time.Time) time.Time {
	if !t.Enabled {
		return time.Time{}
	}
	spec, err := cronx.Parse(t.Cron)
	if err != nil {
		return time.Time{}
	}
	unit := time.Minute
	if spec.HasSeconds() {
		unit = time.Second
	}
	return spec.Next(now.Truncate(unit).Add(unit))
}

// RunRecord 一次运行的历史记录。
type RunRecord struct {
	RunID      string `json:"run_id,omitempty"`
	TaskID     string `json:"task_id"`
	StartedAt  string `json:"started_at"`       // RFC3339
	DurationMs int64  `json:"duration_ms"`      // 耗时毫秒
	Status     string `json:"status"`           // success/failed/timeout/canceled/skipped
	ExitCode   int    `json:"exit_code"`        // 进程退出码；启动失败为 -1
	Output     string `json:"output,omitempty"` // stdout+stderr 合并尾部（截断）
	Trigger    string `json:"trigger"`          // cron=调度触发 / manual=手动执行
}

// FailureEvent 是任务最终失败/超时时发出的系统级告警事件。
type FailureEvent struct {
	TaskID     string `json:"task_id"`
	TaskName   string `json:"task_name"`
	RunID      string `json:"run_id"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Trigger    string `json:"trigger"`
}
