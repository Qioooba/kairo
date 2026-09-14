package wscodegen

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"kairo/internal/sysutil"
)

// T052: 工具在 staging 中写了一个文件后失败退出非0。
// 验证最终目标目录保持原样，旧文件内容和哈希摘要完全一致。
func TestToolIsolation_T052_FailureDoesNotCorruptTargetDir(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "output")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 在最终目录中预先放置旧文件
	oldFilePath := filepath.Join(outDir, "OldService.java")
	oldContent := []byte("package com.example;\npublic class OldService { /* untouched */ }")
	if err := os.WriteFile(oldFilePath, oldContent, 0o644); err != nil {
		t.Fatal(err)
	}
	oldHash := sha256.Sum256(oldContent)

	// 创建一个模拟失败的生成器脚本：先在当前工作目录写一个文件，然后以 code 1 失败退出
	var scriptPath string
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(tempDir, "fail_tool.bat")
		scriptContent := "@echo off\r\necho dirty > PartialOutput.java\r\nexit /b 1\r\n"
		if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		scriptPath = filepath.Join(tempDir, "fail_tool.sh")
		scriptContent := "#!/bin/sh\necho dirty > PartialOutput.java\nexit 1\n"
		if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	req := Request{
		Mode:        ModeTool,
		Engine:      EngineJAXWS,
		OutputDir:   outDir,
		Overwrite:   true,
		WSDLContent: sampleWSDL,
	}

	// 使用伪造的 wsimport 指向失败脚本
	req.JDKHome = tempDir
	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeWsimport := filepath.Join(binDir, wsimportBinName())
	if runtime.GOOS == "windows" {
		// 在 Windows 上 wsimport 是 .exe 或 .bat
		if err := os.WriteFile(fakeWsimport, []byte("@echo off\r\necho dirty > PartialOutput.java\r\nexit /b 1\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.WriteFile(fakeWsimport, []byte("#!/bin/sh\necho dirty > PartialOutput.java\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// 伪造 java 二进制以通过 InspectJDKHome
	fakeJava := filepath.Join(binDir, javaBinName())
	if runtime.GOOS == "windows" {
		_ = os.WriteFile(fakeJava, []byte("@echo off\r\necho openjdk version \"1.8.0\"\r\n"), 0o755)
	} else {
		_ = os.WriteFile(fakeJava, []byte("#!/bin/sh\necho openjdk version \"1.8.0\"\n"), 0o755)
	}

	_, err := GenerateContext(context.Background(), req, nil)
	if err == nil {
		t.Fatalf("预期的工具生成失败未发生，err 应非 nil")
	}

	// 校验：最终目录中的旧文件未被篡改，PartialOutput.java 绝对未出现在最终目录
	afterContent, err := os.ReadFile(oldFilePath)
	if err != nil {
		t.Fatalf("读取旧文件失败: %v", err)
	}
	afterHash := sha256.Sum256(afterContent)
	if afterHash != oldHash {
		t.Errorf("旧文件摘要发生改变！before=%x, after=%x", oldHash, afterHash)
	}

	partialDest := filepath.Join(outDir, "PartialOutput.java")
	if _, err := os.Stat(partialDest); err == nil {
		t.Errorf("失败工具写入的文件 PartialOutput.java 泄露到了最终目录 %s！", outDir)
	}
}

// T053: 子进程树超时和取消时，整棵进程树能够干净退出，不残留孤儿进程。
func TestToolIsolation_T053_ProcessTreeTermination(t *testing.T) {
	tempDir := t.TempDir()

	// 构造一个会派生长时间休眠孙进程的脚本
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		scriptPath := filepath.Join(tempDir, "sleeper_tree.bat")
		// 启动一个子进程 ping 127.0.0.1 阻塞等待
		script := "@echo off\r\nping -n 30 127.0.0.1 > nul\r\n"
		if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("cmd.exe", "/c", scriptPath)
	} else {
		scriptPath := filepath.Join(tempDir, "sleeper_tree.sh")
		script := "#!/bin/sh\nsleep 30\n"
		if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("/bin/sh", scriptPath)
	}

	cmd.Dir = tempDir
	sysutil.HideConsoleWindow(cmd)

	proc, err := sysutil.StartManagedCommand(cmd)
	if err != nil {
		t.Fatalf("StartManagedCommand 失败: %v", err)
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- proc.Wait()
	}()

	// 启动后给子进程一点启动时间，然后执行 KillTree
	time.Sleep(100 * time.Millisecond)

	startKill := time.Now()
	if err := proc.KillTree(); err != nil {
		t.Logf("KillTree 返回提示: %v", err)
	}
	_ = proc.Close()

	// 等待进程退出，必须在 3 秒内完全退出，不能等 30 秒
	select {
	case <-waitDone:
		dur := time.Since(startKill)
		if dur > 5*time.Second {
			t.Errorf("KillTree 耗时过长 (%v)，未能迅速杀死进程树", dur)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("KillTree 后 5 秒内子进程树未能退出，存在孤儿进程卡死！")
	}
}

// T054: 并发测试：同目录互斥不覆盖；不同目录可并行。
func TestToolIsolation_T054_ConcurrencyAndDirectoryLocking(t *testing.T) {
	tempDir := t.TempDir()
	dirSame := filepath.Join(tempDir, "target_same")
	dirOther := filepath.Join(tempDir, "target_other")

	_ = os.MkdirAll(dirSame, 0o755)
	_ = os.MkdirAll(dirOther, 0o755)

	// 1. 同目录互斥测试：两个 goroutine 锁同一个目录，验证互斥
	lock1 := targetDirLock.acquire(dirSame)
	acquiredSecond := make(chan struct{})
	go func() {
		lock2 := targetDirLock.acquire(dirSame)
		close(acquiredSecond)
		lock2()
	}()

	// 确认在 lock1 释放前，lock2 不会被获取
	select {
	case <-acquiredSecond:
		t.Fatalf("同目录锁未互斥：lock1 尚未释放，lock2 已被获取！")
	case <-time.After(50 * time.Millisecond):
		// 正确阻塞
	}
	lock1()

	select {
	case <-acquiredSecond:
		// 成功解除阻塞并获取
	case <-time.After(1 * time.Second):
		t.Fatalf("lock1 释放后，lock2 未能成功获取")
	}

	// 2. 不同目录并行测试：锁不同目录，两者不互相阻塞
	lockA := targetDirLock.acquire(dirSame)
	acquiredOther := make(chan struct{})
	go func() {
		lockB := targetDirLock.acquire(dirOther)
		close(acquiredOther)
		lockB()
	}()

	select {
	case <-acquiredOther:
		// 成功并行获取，未被 lockA 阻塞
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("不同目录之间发生了错误阻塞！")
	}
	lockA()

	// 3. 高并发 writeFiles 压力测试
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			d := filepath.Join(tempDir, fmt.Sprintf("bench_dir_%d", idx%3))
			_ = os.MkdirAll(d, 0o755)
			files := []GeneratedFile{
				{RelPath: "Hello.java", Content: fmt.Sprintf("public class Hello%d {}", idx), Kind: "java"},
			}
			_, _ = writeFiles(d, files, true)
		}(i)
	}
	wg.Wait()
}
