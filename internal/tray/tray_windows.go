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
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "退出Kairo")

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					if cfg.OnOpenBrowser != nil {
						cfg.OnOpenBrowser()
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
