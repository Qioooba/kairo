package upgrade

// lock_concurrency_test.go — OTH-01「升级旧锁回收破坏互斥」的回归测试。
//
// 审计复现的问题：`acquireLock` 在创建失败后读取锁文件里的 PID，判断进程已死就
// `os.Remove(path)` 再重新 `O_CREATE|O_EXCL` 创建。删除动作与"刚才读到的旧 PID 判断"
// 之间存在读后删除窗口，因此两个进程可以同时成功持锁：
//
//	A 删旧锁 → A 建新锁 → B 仍按旧 PID 判断删掉 A 的新锁 → B 也建锁成功
//
// 根因是"互斥正确性取决于锁文件里的 PID 元数据"，所以这里有两类用例：
//
//  1. 互斥性质/并发用例（doc 验收口径）：
//     TestAuditConcurrentStaleLockReclamation（同进程 64 goroutine）、
//     TestAuditIndependentProcessStaleLock（16 个真实独立子进程 + start gate 放行）。
//  2. 确定性交错用例（锁定根因，必然观察到两个同时持锁者）：
//     TestAuditLockOwnershipMustNotDependOnMetadata（同进程）、
//     TestAuditIndependentProcessLockNotStolenByStaleMetadata（跨进程）。
//     两者都只在锁文件仍是"元数据即互斥"的旧实现下失败。
//
// 统计口径统一为"同一时刻的成功持锁者数量"，而不是检查源码字符串。

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// auditDeadPID 是一个必然不存在的进程号（超过 Unix pid_max 默认上限，
// 也不是 Windows 上的合法 PID）。旧实现依据"锁文件中的 PID 已死"回收锁，
// 本文件用它构造审计的前置条件：崩溃后遗留的陈旧锁文件。
const auditDeadPID = 2147483644

// auditStaleLockContent 是"持有者已死"的锁文件内容。
func auditStaleLockContent() []byte {
	return []byte("pid=" + strconv.Itoa(auditDeadPID) +
		"\ntoken=00000000000000000000000000000000" +
		"\nstarted_at=2026-09-02T10:00:00Z\n")
}

// writeStaleDeadPIDLock 写入一个"持有者已死"的锁文件。
func writeStaleDeadPIDLock(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, auditStaleLockContent(), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}
}

// rewriteStaleDeadPIDLock 尝试在持锁期间把锁文件元数据改写成"死 PID"。
// 在真正的内核锁下这一步可能直接失败（Windows LockFileEx 会阻止其它进程写入
// 被锁区间），失败本身也说明互斥有效，因此调用方必须容忍错误。
func rewriteStaleDeadPIDLock(path string) error {
	return os.WriteFile(path, auditStaleLockContent(), 0o600)
}

// TestAuditStaleLockIsReclaimable 验证审计夹具与验收前提同时成立：
// 崩溃后遗留的"死 PID"锁文件必须能被下一个进程直接接管，无需人工删文件。
func TestAuditStaleLockIsReclaimable(t *testing.T) {
	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, lockFileName)
	writeStaleDeadPIDLock(t, lockPath)
	release, err := acquireLock(lockPath, fixedNow())
	if err != nil {
		t.Fatalf("陈旧锁必须可被接管（不得遗留永久死锁），得到错误: %v", err)
	}
	release()
}

// TestAuditLockDiagnosticsAreWritten 确认 PID/token/时间戳仍在锁文件里可见
// （只作诊断用，不参与互斥判定）。写入发生在持锁句柄上，同时也验证了
// "Windows 上被锁区间只能由加锁的那个文件对象访问"这条约束没有把诊断写入搞坏。
//
// 注意：持锁期间其它句柄读不到被锁区间（Windows 行为），所以要在 release 之后读；
// 锁文件路径稳定、不会被 unlink，release 后内容仍在。
func TestAuditLockDiagnosticsAreWritten(t *testing.T) {
	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, lockFileName)
	release, err := acquireLock(lockPath, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	release()

	raw, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("锁文件必须保持可读（路径稳定、不 unlink）: %v", err)
	}
	content := string(raw)
	for _, want := range []string{
		"pid=" + strconv.Itoa(os.Getpid()),
		"token=",
		"started_at=" + fixedNow().UTC().Format(time.RFC3339Nano),
	} {
		if !strings.Contains(content, want) {
			t.Errorf("锁文件诊断信息缺少 %q，实际内容:\n%s", want, content)
		}
	}
}

// TestAuditConcurrentStaleLockReclamation 用 64 个 goroutine 争用同一个陈旧锁文件。
// 统计在所有尝试都结束后才进行，因此计入的就是"同一时刻真正持锁"的竞争者数量。
func TestAuditConcurrentStaleLockReclamation(t *testing.T) {
	const (
		contenders = 64
		rounds     = 40
	)
	for round := 1; round <= rounds; round++ {
		dataDir := t.TempDir()
		lockPath := filepath.Join(dataDir, lockFileName)
		writeStaleDeadPIDLock(t, lockPath)

		var (
			start    = make(chan struct{})
			wg       sync.WaitGroup
			mu       sync.Mutex
			holders  []int
			releases []func()
		)
		for i := 0; i < contenders; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				<-start
				release, err := acquireLock(lockPath, fixedNow())
				if err != nil {
					return
				}
				mu.Lock()
				holders = append(holders, id)
				releases = append(releases, release)
				mu.Unlock()
			}(i)
		}
		close(start)
		wg.Wait()

		held := len(holders)
		holdersCopy := append([]int(nil), holders...)
		// 所有争用者都已结束尝试后才释放，保证统计到的是并发持有而不是先后持有。
		for _, release := range releases {
			release()
		}
		if held > 1 {
			t.Fatalf("round %d: mutual exclusion broken, %d simultaneous lock holders (goroutines=%v)",
				round, held, holdersCopy)
		}
	}
}

// TestAuditLockOwnershipMustNotDependOnMetadata 锁定 OTH-01 的根因：
// 旧实现的互斥只由"锁文件里的 PID 是否存活"决定。持有者 A 已经真实持锁，
// 但只要文件元数据声称 PID 已死（PID 复用 / 写入中途 / 被外部改写），
// 第二个 acquireLock 就能删掉 A 的锁并"也"成功 —— 出现两个同时持锁者。
// 修复后内核锁才是互斥依据，元数据不再参与判定。
func TestAuditLockOwnershipMustNotDependOnMetadata(t *testing.T) {
	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, lockFileName)
	writeStaleDeadPIDLock(t, lockPath)

	releaseA, err := acquireLock(lockPath, fixedNow())
	if err != nil {
		t.Fatalf("A 接管陈旧锁失败: %v", err)
	}
	defer releaseA()

	// 元数据只是诊断信息：改写成"死 PID"后，A 的持有权不得消失。
	rewriteErr := rewriteStaleDeadPIDLock(lockPath)

	releaseB, errB := acquireLock(lockPath, fixedNow())
	if errB == nil {
		// B 成功即代表两个同时持锁者；先释放再报告，避免影响后续用例。
		releaseB()
		releaseA()
		t.Fatalf("互斥取决于元数据：A 仍持锁期间 B 也成功持锁（同时持锁者=2, 元数据改写错误=%v）", rewriteErr)
	}
}

// TestAuditIndependentProcessLockNotStolenByStaleMetadata 用两个真实独立进程
// 复现同一条根因，排除"只在同进程内成立"的假设：
//
//	C1 先接管陈旧锁并持续持有（等 release gate）→ 元数据被改写成"死 PID"
//	→ C2 启动争锁。C2 成功即出现两个同时持锁的进程。
func TestAuditIndependentProcessLockNotStolenByStaleMetadata(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, lockFileName)
	writeStaleDeadPIDLock(t, lockPath)
	startGate := filepath.Join(dataDir, "start.gate")
	releaseGate := filepath.Join(dataDir, "release.gate")

	var children []*exec.Cmd
	defer func() {
		_ = os.WriteFile(releaseGate, []byte("release"), 0o600)
		for _, cmd := range children {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		}
	}()

	// C1：接管陈旧锁并持续持有。
	logC1 := filepath.Join(dataDir, "c1.log")
	envC1 := auditChildEnv(lockPath, startGate, releaseGate, logC1,
		filepath.Join(dataDir, "ready.c1"), "wait")
	children = append(children, startAuditChild(t, exe, envC1))
	if err := os.WriteFile(startGate, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	linesC1, err := waitForAuditLines(logC1, 1, 120*time.Second)
	if err != nil {
		t.Fatalf("C1 未取得锁: %v (lines=%v)", err, linesC1)
	}
	if !strings.HasPrefix(linesC1[0], "held ") {
		t.Fatalf("C1 必须取得锁才能继续本用例，得到 %q", linesC1[0])
	}

	// 元数据被改写成"死 PID"（可能是 PID 复用、写入中途损坏或外部改写）。
	rewriteErr := rewriteStaleDeadPIDLock(lockPath)

	// C2：在 C1 仍持锁（release gate 尚未出现）时争锁。
	logC2 := filepath.Join(dataDir, "c2.log")
	envC2 := auditChildEnv(lockPath, startGate, releaseGate, logC2,
		filepath.Join(dataDir, "ready.c2"), "attempt")
	children = append(children, startAuditChild(t, exe, envC2))
	linesC2, err := waitForAuditLines(logC2, 1, 120*time.Second)
	if err != nil {
		t.Fatalf("C2 未给出争锁结果: %v", err)
	}
	// C2 结束时 C1 仍在等待 release gate，因此此时两行 "held" 就是同时持锁。
	if strings.HasPrefix(linesC2[0], "held ") {
		t.Fatalf("互斥跨进程被元数据破坏：C1=%q 与 C2=%q 同时成功持锁（元数据改写错误=%v）",
			linesC1[0], linesC2[0], rewriteErr)
	}
}

// TestAuditLockReleasedByKilledHolder 覆盖 doc 验收："杀死持锁子进程后，
// 另一个进程能获得锁并进入已有恢复流程，无需手删文件"。
func TestAuditLockReleasedByKilledHolder(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, lockFileName)
	startGate := filepath.Join(dataDir, "start.gate")
	releaseGate := filepath.Join(dataDir, "release.gate")

	holderLog := filepath.Join(dataDir, "holder.log")
	env := auditChildEnv(lockPath, startGate, releaseGate, holderLog,
		filepath.Join(dataDir, "ready.holder"), "wait")
	cmd := startAuditChild(t, exe, env)
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	if err := os.WriteFile(startGate, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := waitForAuditLines(holderLog, 1, 120*time.Second)
	if err != nil || !strings.HasPrefix(lines[0], "held ") {
		t.Fatalf("子进程未取得锁: err=%v lines=%v", err, lines)
	}

	// 强杀持锁进程：内核锁必须随进程消失而释放，不能留下需要手删文件的死锁。
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill holder: %v", err)
	}
	_ = cmd.Wait()

	release, err := acquireLock(lockPath, fixedNow())
	if err != nil {
		t.Fatalf("持有者被杀死后必须能直接取得锁（无需手删文件），得到: %v", err)
	}
	release()
}

// TestAuditIndependentProcessStaleLock 用 16 个真实独立进程争用同一个陈旧锁文件。
// 每个子进程无论成功还是失败都只写一行日志，父进程等满 16 行后才统计 "held" 行数，
// 因此计数结果就是"同一时刻真正持锁的进程数"。
func TestAuditIndependentProcessStaleLock(t *testing.T) {
	const (
		processes = 16
		rounds    = 12
	)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}

	for round := 1; round <= rounds; round++ {
		dataDir := t.TempDir()
		lockPath := filepath.Join(dataDir, lockFileName)
		writeStaleDeadPIDLock(t, lockPath)
		startGate := filepath.Join(dataDir, "start.gate")
		releaseGate := filepath.Join(dataDir, "release.gate")
		holderLog := filepath.Join(dataDir, "holders.log")

		cmds := make([]*exec.Cmd, 0, processes)
		cleanup := func() {
			_ = os.WriteFile(releaseGate, []byte("release"), 0o600)
			for _, cmd := range cmds {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				_ = cmd.Wait()
			}
		}
		for i := 0; i < processes; i++ {
			env := auditChildEnv(lockPath, startGate, releaseGate, holderLog,
				filepath.Join(dataDir, "ready."+strconv.Itoa(i)), "wait")
			cmd := exec.Command(exe, "-test.run=^TestAuditLockHolderSubprocess$", "-test.timeout=300s")
			cmd.Env = env
			if err := cmd.Start(); err != nil {
				cleanup()
				t.Fatalf("round %d: start subprocess %d: %v", round, i, err)
			}
			cmds = append(cmds, cmd)
		}

		// 等全部子进程都进入 start gate 自旋后再放行，让 16 次 acquireLock 尽量同时发生。
		for i := 0; i < processes; i++ {
			readyFile := filepath.Join(dataDir, "ready."+strconv.Itoa(i))
			if err := waitForAuditFile(readyFile, 120*time.Second); err != nil {
				cleanup()
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if err := os.WriteFile(startGate, []byte("go"), 0o600); err != nil {
			cleanup()
			t.Fatal(err)
		}

		lines, waitErr := waitForAuditLines(holderLog, processes, 120*time.Second)
		var heldPIDs []int
		var settleErr string
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "held" {
				if pid, convErr := strconv.Atoi(fields[1]); convErr == nil {
					heldPIDs = append(heldPIDs, pid)
				}
			}
		}

		// 无论是否检测到违规都必须放行子进程，否则用例会挂死。
		_ = os.WriteFile(releaseGate, []byte("release"), 0o600)
		for _, cmd := range cmds {
			if err := cmd.Wait(); err != nil {
				if settleErr == "" {
					settleErr = err.Error()
				}
			}
		}

		if waitErr != nil {
			t.Fatalf("round %d: %v (lines=%v subprocess=%s)", round, waitErr, lines, settleErr)
		}
		if len(heldPIDs) > 1 {
			t.Fatalf("round %d: mutual exclusion broken across processes, %d simultaneous lock holders (pids=%v, lines=%v)",
				round, len(heldPIDs), heldPIDs, lines)
		}
	}
}

// TestAuditUpgradeRunnerSubprocess 是"两个竞争启动流程"用例的子进程入口：
// 它直接调用产品入口 Run（而不是自己调 acquireLock），并让先拿到锁的流程在
// 临界区里停留一段时间，使得另一个流程必然撞锁。
func TestAuditUpgradeRunnerSubprocess(t *testing.T) {
	dataDir := os.Getenv("KAIRO_AUDIT_RUN_DATA_DIR")
	if dataDir == "" {
		t.Skip("subprocess entry point only")
	}
	assetPath := os.Getenv("KAIRO_AUDIT_RUN_ASSET")
	readyFile := os.Getenv("KAIRO_AUDIT_RUN_READY")
	startGate := os.Getenv("KAIRO_AUDIT_START_GATE")
	runLog := os.Getenv("KAIRO_AUDIT_RUN_LOG")

	if err := os.WriteFile(readyFile, []byte("ready"), 0o600); err != nil {
		t.Fatalf("write ready file: %v", err)
	}
	if err := spinForAuditFile(startGate, 120*time.Second); err != nil {
		t.Fatalf("wait for start gate: %v", err)
	}

	assets := []Asset{{
		Name: "audit-asset", Path: assetPath, CurrentVersion: 2, Critical: true,
		Validate:      ValidateJSON,
		DetectVersion: detectJSONVersion,
		Migrations: map[int]Migration{1: func([]byte) ([]byte, error) {
			// 让先持锁的流程在临界区内停留，保证并发的另一个流程撞到锁。
			time.Sleep(300 * time.Millisecond)
			return []byte(`{"version":2,"name":"audit"}`), nil
		}},
	}}
	_, err := Run(Options{DataDir: dataDir, ProductVersion: "v0.18", Now: time.Now, Assets: assets})
	if err != nil {
		appendAuditLogLine(runLog, "failed "+err.Error())
		return
	}
	appendAuditLogLine(runLog, "committed")
}

// TestAuditCompetingUpgradeProcessesCommitOnce 用两个真实进程同时启动产品入口 Run。
// 验收口径不是"两个都返回成功"，而是：任意时刻只有一个流程在临界区内，
// 因此只出现一次完整提交，另一个流程必须明确失败。
func TestAuditCompetingUpgradeProcessesCommitOnce(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	dataDir := t.TempDir()
	assetPath := filepath.Join(dataDir, "audit.json")
	if err := os.WriteFile(assetPath, []byte(`{"version":1,"name":"audit"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	startGate := filepath.Join(dataDir, "start.gate")
	runLog := filepath.Join(dataDir, "runs.log")

	var children []*exec.Cmd
	defer func() {
		for _, cmd := range children {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		}
	}()

	const runners = 2
	for i := 0; i < runners; i++ {
		cmd := exec.Command(exe, "-test.run=^TestAuditUpgradeRunnerSubprocess$", "-test.timeout=300s")
		cmd.Env = append(os.Environ(),
			"KAIRO_AUDIT_RUN_DATA_DIR="+dataDir,
			"KAIRO_AUDIT_RUN_ASSET="+assetPath,
			"KAIRO_AUDIT_RUN_READY="+filepath.Join(dataDir, "ready.run."+strconv.Itoa(i)),
			"KAIRO_AUDIT_START_GATE="+startGate,
			"KAIRO_AUDIT_RUN_LOG="+runLog,
		)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start runner %d: %v", i, err)
		}
		children = append(children, cmd)
	}
	for i := 0; i < runners; i++ {
		ready := filepath.Join(dataDir, "ready.run."+strconv.Itoa(i))
		if err := waitForAuditFile(ready, 120*time.Second); err != nil {
			t.Fatalf("runner %d not ready: %v", i, err)
		}
	}
	if err := os.WriteFile(startGate, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}

	lines, waitErr := waitForAuditLines(runLog, runners, 180*time.Second)
	// 先等子进程退出，再判定，避免把仍在运行的流程算进来。
	for _, cmd := range children {
		_ = cmd.Wait()
	}
	if waitErr != nil {
		t.Fatalf("升级流程未给出结果: %v (lines=%v)", waitErr, lines)
	}

	committed := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "committed") {
			committed++
		}
	}
	t.Logf("并发升级流程结果: %v", lines)
	if committed != 1 {
		t.Fatalf("必须恰好一次完整提交，实际 %d 次成功 (lines=%v)", committed, lines)
	}

	raw, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"version":2`) {
		t.Fatalf("升级结果不正确: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(dataDir, manifestFileName)); err != nil {
		t.Fatalf("成功提交后必须存在升级清单: %v", err)
	}
}

// TestAuditLockHolderSubprocess 是上面几个用例拉起的子进程入口。
// 缺少环境变量时跳过，保证它作为普通测试单独运行时没有副作用。
//
// KAIRO_AUDIT_MODE=wait    取得锁后一直持有，直到 release gate 出现（默认）
// KAIRO_AUDIT_MODE=attempt 只报告争锁结果，随即结束
func TestAuditLockHolderSubprocess(t *testing.T) {
	lockPath := os.Getenv("KAIRO_AUDIT_LOCK_PATH")
	if lockPath == "" {
		t.Skip("subprocess entry point only")
	}
	startGate := os.Getenv("KAIRO_AUDIT_START_GATE")
	releaseGate := os.Getenv("KAIRO_AUDIT_RELEASE_GATE")
	holderLog := os.Getenv("KAIRO_AUDIT_HOLDER_LOG")
	readyFile := os.Getenv("KAIRO_AUDIT_READY_FILE")
	mode := os.Getenv("KAIRO_AUDIT_MODE")

	// 先报告"已就绪并进入自旋"，父进程等齐全部就绪后才放行，
	// 这样多个进程的 acquireLock 才会真正同时到达争用点。
	if readyFile != "" {
		if err := os.WriteFile(readyFile, []byte("ready"), 0o600); err != nil {
			t.Fatalf("write ready file: %v", err)
		}
	}
	if err := spinForAuditFile(startGate, 120*time.Second); err != nil {
		t.Fatalf("wait for start gate: %v", err)
	}

	release, err := acquireLock(lockPath, time.Now())
	if err != nil {
		appendAuditLogLine(holderLog, "lost "+strconv.Itoa(os.Getpid()))
		return
	}
	// 先把自己的 PID 记进日志，再按模式决定是否继续持有。
	appendAuditLogLine(holderLog, "held "+strconv.Itoa(os.Getpid()))
	if mode != "attempt" {
		if err := waitForAuditFile(releaseGate, 120*time.Second); err != nil {
			t.Fatalf("wait for release gate: %v", err)
		}
	}
	release()
}

// auditChildEnv 构造子进程环境变量。
func auditChildEnv(lockPath, startGate, releaseGate, holderLog, readyFile, mode string) []string {
	return append(os.Environ(),
		"KAIRO_AUDIT_LOCK_PATH="+lockPath,
		"KAIRO_AUDIT_START_GATE="+startGate,
		"KAIRO_AUDIT_RELEASE_GATE="+releaseGate,
		"KAIRO_AUDIT_HOLDER_LOG="+holderLog,
		"KAIRO_AUDIT_READY_FILE="+readyFile,
		"KAIRO_AUDIT_MODE="+mode,
	)
}

// startAuditChild 启动一个持锁/争锁子进程。
func startAuditChild(t *testing.T, exe string, env []string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(exe, "-test.run=^TestAuditLockHolderSubprocess$", "-test.timeout=300s")
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}
	return cmd
}

// spinForAuditFile 不睡眠地自旋等待文件出现：用于让多个子进程在同一微秒级窗口内放行。
func spinForAuditFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s", path)
		}
		runtime.Gosched()
	}
}

// waitForAuditFile 轮询等待文件出现。
func waitForAuditFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s", path)
		}
		time.Sleep(time.Millisecond)
	}
}

// appendAuditLogLine 以追加方式写一行日志并立即落盘（供其他进程读取）。
func appendAuditLogLine(path, line string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line + "\n")
	_ = f.Close()
}

// waitForAuditLines 轮询等待日志文件累计到 want 行。
func waitForAuditLines(path string, want int, timeout time.Duration) ([]string, error) {
	deadline := time.Now().Add(timeout)
	var lines []string
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			lines = lines[:0]
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.TrimSpace(line) != "" {
					lines = append(lines, strings.TrimSpace(line))
				}
			}
			if len(lines) >= want {
				return lines, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return lines, err
		}
		if time.Now().After(deadline) {
			return lines, fmt.Errorf("timeout waiting for %d log lines, got %d", want, len(lines))
		}
		time.Sleep(2 * time.Millisecond)
	}
}
