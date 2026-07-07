//go:build windows

package tray

import (
	"os"
	"unsafe"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
)

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

const mbIconError = 0x00000010

// run 启动 Windows 系统托盘。阻塞主线程直到用户选择退出。
func run(cfg Config) {
	systray.Run(func() {
		systray.SetIcon(iconBytes)
		if cfg.Tooltip != "" {
			systray.SetTooltip(cfg.Tooltip)
		}

		mOpen := systray.AddMenuItem("打开浏览器", "在默认浏览器中打开")

		// v1.0 便笺提醒的"暂停今日 / 恢复"菜单项。
		// label 动态：未暂停时显示"暂停今日提醒"，暂停后切到"恢复提醒"。
		// systray.MenuItem 没有公开 Title()，所以用 paused bool 外部变量追踪状态。
		var mPause *systray.MenuItem
		var paused bool
		if cfg.OnPauseToday != nil || cfg.OnResumeToday != nil {
			mPause = systray.AddMenuItem("暂停今日提醒", "今天到次日 0 点不再弹提醒")
			systray.AddSeparator()
		}

		mQuit := systray.AddMenuItem("退出", "退出Kairo")

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					if cfg.OnOpenBrowser != nil {
						cfg.OnOpenBrowser()
					}
				case <-mPause.ClickedCh:
					if mPause == nil {
						continue
					}
					if !paused {
						if cfg.OnPauseToday != nil {
							cfg.OnPauseToday()
						}
						mPause.SetTitle("恢复提醒")
						mPause.SetTooltip("立即恢复所有提醒")
						paused = true
					} else {
						if cfg.OnResumeToday != nil {
							cfg.OnResumeToday()
						}
						mPause.SetTitle("暂停今日提醒")
						mPause.SetTooltip("今天到次日 0 点不再弹提醒")
						paused = false
					}
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, func() {
		if cfg.OnQuit != nil {
			cfg.OnQuit()
		}
	})
}

// fatalDialog 弹出 Windows MessageBox 显示错误，写 crash.log，然后退出。
// MessageBox 支持 Ctrl+C 复制整个对话框文本（Windows 原生特性）。
func fatalDialog(msg string) {
	writeCrashLog(msg)

	text, _ := windows.UTF16PtrFromString(msg)
	caption, _ := windows.UTF16PtrFromString("Kairo启动失败")
	procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(caption)),
		mbIconError,
	)
	os.Exit(1)
}
