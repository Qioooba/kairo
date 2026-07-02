package httpserver

import (
	"errors"
	"strings"
	"testing"

	"kairo/internal/config"
)

// mockAutostartOps 实现 autostartOps interface，用于单测 autoStartPutCore 的
// 决策逻辑（回滚路径、错误处理、audit 写入）。所有调用都被记录下来供断言。
type mockAutostartOps struct {
	// 可注入的失败：让某个操作返回指定 error
	setAutoStartErr     error // 每次 SetAutoStart 都返这个 err;优先于 setAutoStartErrs
	setAutoStartErrs    []error // 按序返错（第一次返 errs[0], 第二次返 errs[1]...）;留尾后用最后一个
	isAutoStartEnabledV bool
	isAutoStartEnabledE error
	replaceConfigErr    error
	currentExeV         string
	currentExeE         error
	keyName             string

	// 记录所有调用
	setAutoStartCalls []setAutoStartCall
	replaceCalls      int
	currentExeCalls   int
}

type setAutoStartCall struct {
	Enabled bool
	ExePath string
}

func (m *mockAutostartOps) SetAutoStart(enabled bool, exePath string) error {
	m.setAutoStartCalls = append(m.setAutoStartCalls, setAutoStartCall{enabled, exePath})
	if m.setAutoStartErr != nil {
		return m.setAutoStartErr
	}
	// setAutoStartErrs 按序返错,留尾后用最后一个;空切片返 nil
	if len(m.setAutoStartErrs) == 0 {
		return nil
	}
	idx := len(m.setAutoStartCalls) - 1
	if idx >= len(m.setAutoStartErrs) {
		idx = len(m.setAutoStartErrs) - 1
	}
	return m.setAutoStartErrs[idx]
}

func (m *mockAutostartOps) IsAutoStartEnabled() (bool, error) {
	return m.isAutoStartEnabledV, m.isAutoStartEnabledE
}

func (m *mockAutostartOps) ReplaceConfig(newCfg *config.Config) error {
	m.replaceCalls++
	return m.replaceConfigErr
}

func (m *mockAutostartOps) CurrentExe() (string, error) {
	m.currentExeCalls++
	return m.currentExeV, m.currentExeE
}

func (m *mockAutostartOps) AutoStartKeyName() string {
	return m.keyName
}

// recordingAudit 记录所有 audit 调用。
type recordingAudit struct {
	calls []auditCall
}

type auditCall struct {
	op     string
	fields []any
}

func (r *recordingAudit) write(op string, fields ...any) {
	// 复制 fields 避免后续被改
	f := append([]any{}, fields...)
	r.calls = append(r.calls, auditCall{op, f})
}

// makeConfigWithAutoStart 构造一份指定 auto_start 值的 config，
// 走 Defaults 让其他字段合法（不读 yaml 文件，纯内存构造）。
func makeConfigWithAutoStart(t *testing.T, enabled bool) *config.Config {
	t.Helper()
	cfg := &config.Config{}
	cfg.Defaults()
	cfg.App.AutoStart = enabled
	return cfg
}

// TestAutoStartPutCore_Ok_Enable 正常路径：旧值 false → 新值 true。
// 期望：200 + audit ok + SetAutoStart(true, exe) + ReplaceConfig 调 1 次。
func TestAutoStartPutCore_Ok_Enable(t *testing.T) {
	cfg := makeConfigWithAutoStart(t, false)
	ops := &mockAutostartOps{
		currentExeV:         `/tmp/kairo-test/kairo.exe`,
		isAutoStartEnabledV: true,
		keyName:             `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\KairoOpsToolboxAutoStart`,
	}
	audit := &recordingAudit{}

	req := adminAutoStartPutReq{Enabled: true}
	got := autoStartPutCore(cfg, req, ops, audit.write)

	if got.Status != 200 {
		t.Errorf("status = %d, want 200; body=%v", got.Status, got.Body)
	}
	if got.Body["ok"] != true {
		t.Errorf("body.ok = %v, want true", got.Body["ok"])
	}
	if got.Body["enabled"] != true {
		t.Errorf("body.enabled = %v, want true", got.Body["enabled"])
	}
	if got.Body["actual"] != true {
		t.Errorf("body.actual = %v, want true", got.Body["actual"])
	}

	if len(ops.setAutoStartCalls) != 1 {
		t.Fatalf("SetAutoStart 应调 1 次,实际 %d 次", len(ops.setAutoStartCalls))
	}
	call := ops.setAutoStartCalls[0]
	if !call.Enabled {
		t.Errorf("SetAutoCall.Enabled = false, want true")
	}
	if call.ExePath != `/tmp/kairo-test/kairo.exe` {
		t.Errorf("SetAutoCall.ExePath = %q, want /tmp/kairo-test/kairo.exe", call.ExePath)
	}
	if ops.replaceCalls != 1 {
		t.Errorf("ReplaceConfig 应调 1 次,实际 %d", ops.replaceCalls)
	}
	if len(audit.calls) != 1 || audit.calls[0].op != "admin.autostart.put" {
		t.Errorf("audit 应调 1 次 admin.autostart.put, 实际 %v", audit.calls)
	}
	if audit.calls[0].fields[1] != "ok" {
		t.Errorf("audit result 应为 'ok', 实际 %v", audit.calls[0].fields[1])
	}
}

// TestAutoStartPutCore_Ok_Disable 正常路径：旧值 true → 新值 false。
// 期望：200 + SetAutoStart(false, "") + ReplaceConfig 调 1 次。
// 重点:disable 方向 exePath 是空,SetAutoStart 不应该被传 exePath。
func TestAutoStartPutCore_Ok_Disable(t *testing.T) {
	cfg := makeConfigWithAutoStart(t, true)
	ops := &mockAutostartOps{
		isAutoStartEnabledV: false,
		keyName:             "kairo",
	}
	audit := &recordingAudit{}

	req := adminAutoStartPutReq{Enabled: false}
	got := autoStartPutCore(cfg, req, ops, audit.write)

	if got.Status != 200 {
		t.Errorf("status = %d, want 200; body=%v", got.Status, got.Body)
	}
	if len(ops.setAutoStartCalls) != 1 {
		t.Fatalf("SetAutoStart 应调 1 次,实际 %d", len(ops.setAutoStartCalls))
	}
	call := ops.setAutoStartCalls[0]
	if call.Enabled {
		t.Errorf("SetAutoCall.Enabled = true, want false")
	}
	if call.ExePath != "" {
		t.Errorf("disable 时 exePath 应为空,实际 %q", call.ExePath)
	}
	if ops.replaceCalls != 1 {
		t.Errorf("ReplaceConfig 应调 1 次")
	}
}

// TestAutoStartPutCore_RegistryWriteFail 写注册表失败 → 直接 500,
// 不调 ReplaceConfig,不写 yaml(避免 config 标 true 但注册表实际失败)。
func TestAutoStartPutCore_RegistryWriteFail(t *testing.T) {
	cfg := makeConfigWithAutoStart(t, false)
	ops := &mockAutostartOps{
		currentExeV:     "/tmp/kairo.exe",
		setAutoStartErr: errors.New("permission denied"),
		keyName:         "kairo",
	}
	audit := &recordingAudit{}

	req := adminAutoStartPutReq{Enabled: true}
	got := autoStartPutCore(cfg, req, ops, audit.write)

	if got.Status != 500 {
		t.Errorf("status = %d, want 500", got.Status)
	}
	if !strings.Contains(got.Body["error"].(string), "写入注册表失败") {
		t.Errorf("error 文案应含'写入注册表失败', 实际: %v", got.Body["error"])
	}
	if !strings.Contains(got.Body["error"].(string), "permission denied") {
		t.Errorf("error 文案应包含原始错误: %v", got.Body["error"])
	}
	// 关键断言:ReplaceConfig 没被调(避免 config 标 true 但注册表实际失败)
	if ops.replaceCalls != 0 {
		t.Errorf("ReplaceConfig 不应被调,实际 %d 次", ops.replaceCalls)
	}
	// audit 应记 result=fail
	if len(audit.calls) != 1 || audit.calls[0].fields[1] != "fail" {
		t.Errorf("audit 应记 result=fail, 实际 %v", audit.calls)
	}
}

// TestAutoStartPutCore_YamlFail_DisableRollback **关键**: 旧值 true, 新值 false,
// yaml 写盘失败 → 期望回滚 SetAutoStart(true, exePath),状态恢复到"启用"。
//
// 这是修复 Bug 2 的核心场景:用户想"禁用"自启,注册表已删成功,但 yaml 落盘失败;
// 旧实现直接 200 + warning 让 main.go 下次启动按旧值 true 还原注册表(用户的禁用被吃);
// 新实现回滚注册表到 true,返回 500 + 明确"已回滚,请重试"。
func TestAutoStartPutCore_YamlFail_DisableRollback(t *testing.T) {
	cfg := makeConfigWithAutoStart(t, true) // 旧值:启用
	ops := &mockAutostartOps{
		isAutoStartEnabledV: false, // 写完注册表后回读:确实禁用了
		replaceConfigErr:    errors.New("disk full"),
		keyName:             "kairo",
	}
	audit := &recordingAudit{}

	req := adminAutoStartPutReq{Enabled: false}
	got := autoStartPutCore(cfg, req, ops, audit.write)

	if got.Status != 500 {
		t.Errorf("status = %d, want 500 (yaml 失败时回滚), body=%v", got.Status, got.Body)
	}
	if !strings.Contains(got.Body["error"].(string), "已回滚到原状态") {
		t.Errorf("error 应含'已回滚到原状态', 实际: %v", got.Body["error"])
	}
	if !strings.Contains(got.Body["error"].(string), "启用") {
		t.Errorf("回滚到'启用'方向,error 应含'启用', 实际: %v", got.Body["error"])
	}
	// 关键断言:有 2 次 SetAutoStart 调用:
	//   1) SetAutoStart(false, "")  —— 用户请求禁用
	//   2) SetAutoStart(true, "")   —— 回滚到旧值启用 (旧值是 true,exePath 此时是空)
	if len(ops.setAutoStartCalls) != 2 {
		t.Fatalf("SetAutoStart 应调 2 次 (写新值 + 回滚),实际 %d 次: %+v",
			len(ops.setAutoStartCalls), ops.setAutoStartCalls)
	}
	// 第 1 次:禁用
	if ops.setAutoStartCalls[0].Enabled != false {
		t.Errorf("第 1 次 SetAutoStart 应是禁用, 实际 enabled=%v", ops.setAutoStartCalls[0].Enabled)
	}
	// 第 2 次:回滚到启用(关键 — 旧值是 true)
	if ops.setAutoStartCalls[1].Enabled != true {
		t.Errorf("第 2 次 SetAutoStart 应回滚到启用, 实际 enabled=%v", ops.setAutoStartCalls[1].Enabled)
	}
	// audit:rollback_ok
	if len(audit.calls) != 1 || audit.calls[0].fields[1] != "rollback_ok" {
		t.Errorf("audit 应记 result=rollback_ok, 实际: %+v", audit.calls)
	}
}

// TestAutoStartPutCore_YamlFail_EnableRollback 旧值 false, 新值 true, yaml 失败 →
// 回滚 SetAutoStart(false, ""),状态恢复"禁用"。
// 边界:回滚时 exePath 为空(因为旧值是 false 不需要写注册表,本就没存 exePath)。
func TestAutoStartPutCore_YamlFail_EnableRollback(t *testing.T) {
	cfg := makeConfigWithAutoStart(t, false) // 旧值:禁用
	ops := &mockAutostartOps{
		currentExeV:         "/tmp/kairo.exe",
		isAutoStartEnabledV: true,
		replaceConfigErr:    errors.New("permission denied"),
		keyName:             "kairo",
	}
	audit := &recordingAudit{}

	req := adminAutoStartPutReq{Enabled: true}
	got := autoStartPutCore(cfg, req, ops, audit.write)

	if got.Status != 500 {
		t.Errorf("status = %d, want 500", got.Status)
	}
	if len(ops.setAutoStartCalls) != 2 {
		t.Fatalf("SetAutoStart 应调 2 次,实际 %d", len(ops.setAutoStartCalls))
	}
	// 第 1 次:启用(新值,带 exePath)
	if !ops.setAutoStartCalls[0].Enabled || ops.setAutoStartCalls[0].ExePath == "" {
		t.Errorf("第 1 次应是启用+带 exePath, 实际: %+v", ops.setAutoStartCalls[0])
	}
	// 第 2 次:回滚到禁用(关键 — 旧值 false,exePath 此时应为空,因为回滚是 disable 方向)
	if ops.setAutoStartCalls[1].Enabled != false {
		t.Errorf("第 2 次应回滚到禁用, 实际 enabled=%v", ops.setAutoStartCalls[1].Enabled)
	}
	if ops.setAutoStartCalls[1].ExePath != "" {
		t.Errorf("回滚到禁用时 exePath 应为空, 实际 %q", ops.setAutoStartCalls[1].ExePath)
	}
	// audit:rollback_ok
	if audit.calls[0].fields[1] != "rollback_ok" {
		t.Errorf("audit 应是 rollback_ok, 实际: %v", audit.calls[0].fields[1])
	}
}

// TestAutoStartPutCore_RollbackAlsoFail 极端场景:回滚也失败(注册表被锁 / 杀软拦截)。
// 期望:500 + 状态可能不一致提示 + audit rollback_fail。
//
// 第 1 次 SetAutoStart(写新值)成功,第 2 次(回滚)失败 —— 用 setAutoStartErrs 区分。
func TestAutoStartPutCore_RollbackAlsoFail(t *testing.T) {
	cfg := makeConfigWithAutoStart(t, true)
	ops := &mockAutostartOps{
		isAutoStartEnabledV: false,
		replaceConfigErr:    errors.New("disk full"),
		setAutoStartErrs:    []error{nil, errors.New("registry locked")}, // 第 1 次成功, 第 2 次失败
		keyName:             "kairo",
	}
	audit := &recordingAudit{}

	req := adminAutoStartPutReq{Enabled: false}
	got := autoStartPutCore(cfg, req, ops, audit.write)

	if got.Status != 500 {
		t.Errorf("status = %d, want 500", got.Status)
	}
	if !strings.Contains(got.Body["error"].(string), "状态可能不一致") {
		t.Errorf("error 应提示'状态可能不一致', 实际: %v", got.Body["error"])
	}
	// audit 应记 rollback_fail
	if len(audit.calls) != 1 || audit.calls[0].fields[1] != "rollback_fail" {
		t.Errorf("audit 应记 rollback_fail, 实际: %+v", audit.calls)
	}
	// audit 应包含 rollback_err 字段
	hasRollbackErr := false
	for i, f := range audit.calls[0].fields {
		if f == "rollback_err" && i+1 < len(audit.calls[0].fields) {
			hasRollbackErr = true
		}
	}
	if !hasRollbackErr {
		t.Errorf("audit 应含 rollback_err 字段, 实际: %+v", audit.calls[0].fields)
	}
}

// TestAutoStartPutCore_CurrentExeFail req.Enabled=true 但拿不到 exe 路径 → 500
// 不应调 SetAutoStart 也不应调 ReplaceConfig。
func TestAutoStartPutCore_CurrentExeFail(t *testing.T) {
	cfg := makeConfigWithAutoStart(t, false)
	ops := &mockAutostartOps{
		currentExeE: errors.New("os.Executable failed"),
		keyName:     "kairo",
	}
	audit := &recordingAudit{}

	req := adminAutoStartPutReq{Enabled: true}
	got := autoStartPutCore(cfg, req, ops, audit.write)

	if got.Status != 500 {
		t.Errorf("status = %d, want 500", got.Status)
	}
	if len(ops.setAutoStartCalls) != 0 {
		t.Errorf("拿不到 exe 路径不应调 SetAutoStart, 实际 %d 次", len(ops.setAutoStartCalls))
	}
	if ops.replaceCalls != 0 {
		t.Errorf("拿不到 exe 路径不应调 ReplaceConfig")
	}
}

// TestOldEnabledStr 辅助函数 unit test。
func TestOldEnabledStr(t *testing.T) {
	if oldEnabledStr(true) != "启用" {
		t.Error("oldEnabledStr(true) 应为 '启用'")
	}
	if oldEnabledStr(false) != "禁用" {
		t.Error("oldEnabledStr(false) 应为 '禁用'")
	}
}
