package schedtask

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// catchUpWindow timer 到点后允许的最大迟到时间。
// 超过该窗口（如电脑长时间休眠刚醒）不补跑，避免开机被一堆积压任务轰炸。
const catchUpWindow = 2 * time.Minute

// maxRunsPerTask 每个任务保留的运行历史条数上限。
const maxRunsPerTask = 20

// ErrNotFound 任务不存在。
var ErrNotFound = errors.New("任务不存在")

// ErrRunning 任务正在运行（手动执行时返回；调度触发则自动记 skipped）。
var ErrRunning = errors.New("任务正在运行中，请等本次执行完成")

// View 任务的列表视图：Task + 计算字段。
type View struct {
	Task
	NextRunAt string `json:"next_run_at,omitempty"` // RFC3339
	Running   bool   `json:"running"`               // 是否正在执行
}

// Manager 定时任务总线：内存状态 + 调度器 + 执行器。
//
// 线程模型（与 reminder.Manager 一致）：
//   - 所有公开方法线程安全。
//   - 单 timer 事件驱动：Rebuild 取最近的下次运行挂 time.AfterFunc。
//   - 到点后在独立 goroutine 里跑命令，完成回调里更新状态 + 写历史。
type Manager struct {
	store *Store
	now   func() time.Time // 可注入，便于测试
	// defaultWorkDir 只在任务未显式设置 WorkDir 时生效，不写回任务定义。
	defaultWorkDir string
	ctx            context.Context
	cancel         context.CancelFunc

	mu      sync.Mutex
	items   map[string]*Task
	slots   map[string]time.Time // id → 当前挂起的调度槽（Rebuild 时重算）
	running map[string]bool      // id → 是否有执行中的进程（防重叠）
	runs    map[string][]RunRecord
	timer   *time.Timer
	stopped bool
	wg      sync.WaitGroup
	failureNotifier func(FailureEvent)

	persistMu sync.Mutex // 序列化落盘，避免并发快照互相覆盖
}

// SetFailureNotifier 注册系统级失败通知回调。回调异步执行，通知投递
// 失败不会影响任务状态和调度器生命周期。
func (m *Manager) SetFailureNotifier(fn func(FailureEvent)) {
	m.mu.Lock()
	m.failureNotifier = fn
	m.mu.Unlock()
}

// ManagerOptions 配置 Manager 的运行环境。
type ManagerOptions struct {
	// DefaultWorkDir 是未填写工作目录时的实际执行目录。桌面程序应传可执行文件
	// 所在运行目录，避免 Windows 注册表自启时继承到不确定的当前目录。
	DefaultWorkDir string
}

// NewManager 构造 Manager。加载任务 + 历史 + Rebuild 调度。
func NewManager(store *Store) (*Manager, error) {
	return NewManagerWithOptions(store, ManagerOptions{})
}

// NewManagerWithOptions 构造 Manager，并显式绑定任务的默认运行目录。
func NewManagerWithOptions(store *Store, opts ManagerOptions) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		store:          store,
		now:            time.Now,
		defaultWorkDir: strings.TrimSpace(opts.DefaultWorkDir),
		ctx:            ctx,
		cancel:         cancel,
		items:          make(map[string]*Task),
		slots:          make(map[string]time.Time),
		running:        make(map[string]bool),
		runs:           make(map[string][]RunRecord),
	}
	if m.defaultWorkDir != "" {
		st, err := os.Stat(m.defaultWorkDir)
		if err != nil || !st.IsDir() {
			cancel()
			return nil, fmt.Errorf("默认工作目录不可访问: %s", m.defaultWorkDir)
		}
	}
	loaded, err := store.LoadTasks()
	if err != nil {
		// 损坏 JSON 已被 store 备份 + 返回空，不阻断启动
		fmt.Fprintln(os.Stderr, "WARN: 加载 sched tasks 失败:", err)
	}
	for i := range loaded {
		t := loaded[i]
		// 上次退出时如有任务还在跑（状态卡在 running），标记为失败，
		// 避免前端永远显示"运行中"。
		if t.LastStatus == StatusRunning {
			t.LastStatus = StatusFailed
			t.LastError = "工具重启，上次运行被中断"
		}
		m.items[t.ID] = &t
	}
	runs, err := store.LoadRuns()
	if err != nil {
		fmt.Fprintln(os.Stderr, "WARN: 加载 sched task runs 失败:", err)
	}
	for k, v := range runs {
		m.runs[k] = v
	}
	m.Rebuild()
	return m, nil
}

// NewID 生成新 ID（16 字节随机 hex）。
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// List 返回全部任务视图（按下次运行时间升序；禁用排最后）。
func (m *Manager) List() []View {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	out := make([]View, 0, len(m.items))
	for _, t := range m.items {
		out = append(out, m.viewLocked(t, now))
	}
	sort.Slice(out, func(i, j int) bool {
		ni, nj := out[i].NextRunAt, out[j].NextRunAt
		if (ni == "") != (nj == "") {
			return ni != "" // 有下次运行的排前面
		}
		if ni == "" {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return ni < nj
	})
	return out
}

// Get 按 ID 取一个视图。
func (m *Manager) Get(id string) (View, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.items[id]
	if !ok {
		return View{}, false
	}
	return m.viewLocked(t, m.now()), true
}

// viewLocked 构造视图（已持锁）。
func (m *Manager) viewLocked(t *Task, now time.Time) View {
	v := View{Task: *t, Running: m.running[t.ID]}
	if nr := t.NextRun(now); !nr.IsZero() {
		v.NextRunAt = nr.Format(time.RFC3339)
	}
	return v
}

// Add 新增。返回新分配的任务（含 ID / CreatedAt / UpdatedAt）。
func (m *Manager) Add(in Task) (Task, error) {
	if err := in.Validate(); err != nil {
		return Task{}, err
	}
	now := m.now()
	in.ID = NewID()
	in.Name = strings.TrimSpace(in.Name)
	in.CreatedAt = now.Format(time.RFC3339)
	in.UpdatedAt = in.CreatedAt
	in.LastRunAt, in.LastStatus, in.LastError = "", "", ""
	in.LastDurationMs, in.RunCount = 0, 0

	m.mu.Lock()
	m.items[in.ID] = &in
	m.mu.Unlock()

	if err := m.persistTasks(); err != nil {
		m.mu.Lock()
		delete(m.items, in.ID)
		m.mu.Unlock()
		return Task{}, err
	}
	m.Rebuild()
	return in, nil
}

// Update 全量替换 name/cron/command/work_dir/timeout。
// ID / CreatedAt / 运行态字段（LastRunAt 等）保留原值；Enabled 保留原值
// （启停只能走 Toggle，与 reminder 同一契约，避免双端点竞态）。
func (m *Manager) Update(id string, in Task) (Task, error) {
	m.mu.Lock()
	old, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return Task{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	// 缺省字段回填旧值（PUT 语义下的容错）
	if strings.TrimSpace(in.Name) == "" {
		in.Name = old.Name
	}
	if strings.TrimSpace(in.Command) == "" {
		in.Command = old.Command
	}
	if strings.TrimSpace(in.Cron) == "" {
		in.Cron = old.Cron
	}
	m.mu.Unlock()

	if err := in.Validate(); err != nil {
		return Task{}, err
	}
	m.mu.Lock()
	cur, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return Task{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	in.ID = id
	in.Name = strings.TrimSpace(in.Name)
	in.CreatedAt = cur.CreatedAt
	in.UpdatedAt = m.now().Format(time.RFC3339)
	in.Enabled = cur.Enabled
	in.LastRunAt = cur.LastRunAt
	in.LastStatus = cur.LastStatus
	in.LastDurationMs = cur.LastDurationMs
	in.LastError = cur.LastError
	in.RunCount = cur.RunCount
	m.items[id] = &in
	m.mu.Unlock()

	if err := m.persistTasks(); err != nil {
		m.mu.Lock()
		m.items[id] = cur
		m.mu.Unlock()
		return Task{}, err
	}
	m.Rebuild()
	return in, nil
}

// Delete 删除任务及其运行历史。
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	old, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if m.running[id] {
		m.mu.Unlock()
		return ErrRunning
	}
	oldRuns, hadRuns := m.runs[id]
	delete(m.items, id)
	delete(m.runs, id)
	m.mu.Unlock()

	if err := m.persistTasks(); err != nil {
		m.mu.Lock()
		m.items[id] = old
		if hadRuns {
			m.runs[id] = oldRuns
		}
		m.mu.Unlock()
		return err
	}
	if hadRuns {
		if err := m.persistRuns(); err != nil {
			log.Printf("schedtask: 删除任务历史落盘失败: %v", err)
		}
	}
	m.Rebuild()
	return nil
}

// Toggle 切换 enabled。
func (m *Manager) Toggle(id string) (Task, error) {
	m.mu.Lock()
	t, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return Task{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	t.Enabled = !t.Enabled
	t.UpdatedAt = m.now().Format(time.RFC3339)
	out := *t
	m.mu.Unlock()

	if err := m.persistTasks(); err != nil {
		m.mu.Lock()
		t.Enabled = !t.Enabled
		m.mu.Unlock()
		return Task{}, err
	}
	m.Rebuild()
	return out, nil
}

// RunNow 手动立即执行（异步）。正在运行则返回 ErrRunning。
// 不影响调度节奏（下一次调度照常）。
func (m *Manager) RunNow(id string) error {
	m.mu.Lock()
	t, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	snap := *t
	m.mu.Unlock()
	return m.startRun(snap, "manual")
}

// Runs 返回某任务的运行历史（新的在前）。
func (m *Manager) Runs(id string) ([]RunRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[id]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	list := m.runs[id]
	out := make([]RunRecord, len(list))
	for i, r := range list {
		out[len(list)-1-i] = r // 反转：新的在前
	}
	return out, nil
}

// Rebuild 重新计算所有任务的下次运行 + 重置 timer。
// 在 Add / Update / Delete / Toggle / NewManager / fire 后调用。
func (m *Manager) Rebuild() {
	m.mu.Lock()
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	if m.stopped {
		m.slots = make(map[string]time.Time)
		m.mu.Unlock()
		return
	}
	now := m.now()
	m.slots = make(map[string]time.Time, len(m.items))
	var nextAt time.Time
	for _, t := range m.items {
		nr := t.NextRun(now)
		if nr.IsZero() {
			continue
		}
		m.slots[t.ID] = nr
		if nextAt.IsZero() || nr.Before(nextAt) {
			nextAt = nr
		}
	}
	if nextAt.IsZero() {
		log.Printf("schedtask.Rebuild: 无待运行任务（now=%s）", now.Format("2006-01-02 15:04:05"))
		m.mu.Unlock()
		return
	}
	d := nextAt.Sub(now)
	m.timer = time.AfterFunc(d, m.fire)
	log.Printf("schedtask.Rebuild: 下次运行=%s 距今=%s 任务数=%d",
		nextAt.Format("2006-01-02 15:04:05"), d.Truncate(time.Second), len(m.slots))
	m.mu.Unlock()
}

// fire timer 到点：找出所有"槽已到"的任务并发执行，然后 Rebuild 挂下一次。
func (m *Manager) fire() {
	m.mu.Lock()
	now := m.now()
	var due []Task
	for id, slot := range m.slots {
		if slot.After(now) {
			continue
		}
		if now.Sub(slot) > catchUpWindow {
			log.Printf("schedtask.fire: 跳过 id=%s 槽=%s 超出容差窗口（不补跑）", id, slot.Format("2006-01-02 15:04:05"))
			continue
		}
		t, ok := m.items[id]
		if !ok || !t.Enabled {
			continue
		}
		due = append(due, *t)
	}
	m.mu.Unlock()

	for _, t := range due {
		_ = m.startRun(t, "cron")
	}
	m.Rebuild()
}

// startRun 启动一次执行（异步）。调度触发遇到"还在跑"记 skipped 历史。
func (m *Manager) startRun(t Task, trigger string) error {
	runID := NewID()
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return errors.New("定时任务管理器已停止")
	}
	cur, exists := m.items[t.ID]
	if !exists || (trigger == "cron" && !cur.Enabled) {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, t.ID)
	}
	// fire/RunNow 只负责提交 ID；真正预占运行位时重新读取当前定义，避免
	// 与并发 Update 交错后仍执行旧命令。
	t = *cur
	if m.running[t.ID] {
		m.mu.Unlock()
		// 防重叠：上一次还在跑。调度触发 → 记一条 skipped 历史（便于排查"为什么没跑"）。
		if trigger == "cron" {
			m.appendRun(t.ID, RunRecord{
				RunID:      runID,
				TaskID:    t.ID,
				StartedAt: m.now().Format(time.RFC3339),
				Status:    StatusSkipped,
				ExitCode:  -1,
				Output:    "上一次运行尚未结束，本次跳过",
				Trigger:   trigger,
			})
		}
		return ErrRunning
	}
	// 运行预占位与 WaitGroup.Add 在同一把锁下完成，Stop 先设置 stopped 后
	// 再 Wait，杜绝 Add 与 Wait 并发造成的生命周期竞态。
	m.running[t.ID] = true
	cur.LastStatus = StatusRunning
	cur.LastRunAt = m.now().Format(time.RFC3339)
	cur.LastError = ""
	if strings.TrimSpace(t.WorkDir) == "" {
		t.WorkDir = m.defaultWorkDir
	}
	m.wg.Add(1)
	m.mu.Unlock()

	if err := m.persistTasks(); err != nil {
		log.Printf("schedtask: 状态落盘失败: %v", err)
	}

	go func() {
		defer m.wg.Done()
		start := m.now()
		res := run(m.ctx, &t)
		dur := m.now().Sub(start)

		m.mu.Lock()
		delete(m.running, t.ID)
		errText := ""
		if res.err != nil {
			errText = res.err.Error()
		} else if res.status == StatusFailed && res.exitCode != 0 {
			errText = fmt.Sprintf("退出码 %d", res.exitCode)
		} else if res.status == StatusTimeout {
			errText = fmt.Sprintf("超过 %s 未结束", t.Timeout())
		} else if res.status == StatusCanceled {
			errText = "任务被取消"
		}
		if cur, ok := m.items[t.ID]; ok {
			cur.LastStatus = res.status
			cur.LastDurationMs = dur.Milliseconds()
			cur.LastError = errText
			cur.RunCount++
		}
		m.mu.Unlock()

		m.appendRun(t.ID, RunRecord{
			RunID:      runID,
			TaskID:     t.ID,
			StartedAt:  start.Format(time.RFC3339),
			DurationMs: dur.Milliseconds(),
			Status:     res.status,
			ExitCode:   res.exitCode,
			Output:     res.output,
			Trigger:    trigger,
		})
		if res.status == StatusFailed || res.status == StatusTimeout {
			m.mu.Lock()
			notifier := m.failureNotifier
			m.mu.Unlock()
			if notifier != nil {
				event := FailureEvent{TaskID: t.ID, TaskName: t.Name, RunID: runID, Status: res.status, Error: errText, StartedAt: start.Format(time.RFC3339), FinishedAt: m.now().Format(time.RFC3339), Trigger: trigger}
				go func() { defer func() { _ = recover() }(); notifier(event) }()
			}
		}
		if err := m.persistTasks(); err != nil {
			log.Printf("schedtask: 状态落盘失败: %v", err)
		}
	}()
	return nil
}

// appendRun 追加一条运行历史（裁剪到上限 + 落盘）。
func (m *Manager) appendRun(taskID string, r RunRecord) {
	m.mu.Lock()
	list := append(m.runs[taskID], r)
	if len(list) > maxRunsPerTask {
		list = list[len(list)-maxRunsPerTask:]
	}
	m.runs[taskID] = list
	m.mu.Unlock()
	if err := m.persistRuns(); err != nil {
		log.Printf("schedtask: 运行历史落盘失败: %v", err)
	}
}

// persistTasks 任务定义落盘。
func (m *Manager) persistTasks() error {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.mu.Lock()
	snap := make([]Task, 0, len(m.items))
	for _, t := range m.items {
		snap = append(snap, *t)
	}
	m.mu.Unlock()
	return m.store.SaveTasks(snap)
}

// persistRuns 运行历史落盘。
func (m *Manager) persistRuns() error {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.mu.Lock()
	snap := make(map[string][]RunRecord, len(m.runs))
	for k, v := range m.runs {
		snap[k] = append([]RunRecord(nil), v...)
	}
	m.mu.Unlock()
	return m.store.SaveRuns(snap)
}

// Stop 停止调度并取消、等待全部运行中任务。run 的平台进程控制器保证取消会
// 回收完整进程树，因此 Stop 返回后不会留下 cmd/git/svn/脚本子进程。
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.slots = make(map[string]time.Time)
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
}
