//go:build windows

package popup

import (
	"fmt"
	"log"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// popup_windows.go：自绘右下角悬浮提醒窗口。
//
// v0.18 恢复 v0.13 的自绘方案。用户反馈：Win10 下系统 Toast / 托盘气泡会被
// 「通知设置关闭、专注助手、PowerShell 执行策略」等静默拦截，点"测试"右下角
// 什么都不弹；自绘窗口不受这些系统设置影响，只要桌面在就一定可见。
//
// 设计要点：
//   - 圆角用 SetWindowRgn(CreateRoundRectRgn)，Win7/Win10/Win11 都支持（无需 DWM）。
//   - 半透明用 WS_EX_LAYERED + LWA_ALPHA（整体 alpha）。
//   - 文字用 GDI DrawTextW；主题跟随系统（AppsUseLightTheme）。
//   - 关闭按钮：WM_LBUTTONDOWN 按坐标判断关闭区域。
//   - 多条提醒排队显示，避免堆叠；pump 空闲自动退出，来新提醒再拉起。
//   - 零外部进程（不起 PowerShell）、零 COM，不受系统通知策略影响。

// ---------- 常量 ----------

const (
	popupClassName = "KairoReminderPopupClass_v1"

	popupW      = 360
	popupH      = 140
	popupMargin = 16 // 距工作区右下角
	popupGap    = 1200 * time.Millisecond
	popupLive   = 8 * time.Second
)

const (
	WM_DESTROY     = 0x0002
	WM_PAINT       = 0x000F
	WM_TIMER       = 0x0113
	WM_LBUTTONDOWN = 0x0201
	WM_MOUSEMOVE   = 0x0200

	WS_POPUP         = 0x80000000
	WS_EX_TOPMOST    = 0x00000008
	WS_EX_TOOLWINDOW = 0x00000080
	WS_EX_LAYERED    = 0x00080000
	WS_EX_NOACTIVATE = 0x08000000

	LWA_ALPHA = 0x00000002

	HWND_TOPMOST = ^uintptr(0) // -1

	SWP_NOMOVE     = 0x0002
	SWP_NOSIZE     = 0x0001
	SWP_NOACTIVATE = 0x0010
	SWP_SHOWWINDOW = 0x0040

	SPI_GETWORKAREA = 0x0030

	TRANSPARENT = 1

	DT_SINGLELINE   = 0x00000020
	DT_WORDBREAK    = 0x00000010
	DT_NOPREFIX     = 0x00000800
	DT_END_ELLIPSIS = 0x00008000

	IDC_HAND = 32649

	timerClose = 1

	// 配色（深色主题）
	colBgDark     = 0x001e293b // slate-800
	colAccentDark = 0x006366f1 // indigo-500
	colTitleDark  = 0x00f1f5f9 // slate-100
	colBodyDark   = 0x00cbd5e1 // slate-300
	colCloseDark  = 0x0094a3b8 // slate-400

	// 配色（浅色主题）
	colBgLight     = 0x00fafafa
	colAccentLight = 0x003b82f6 // blue-500
	colTitleLight  = 0x000f172a // slate-900
	colBodyLight   = 0x00334155 // slate-700
	colCloseLight  = 0x0064748b // slate-500
)

// ---------- 结构体 ----------

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       *uint16
}

type paintStruct struct {
	Hdc        uintptr
	FErase     int32
	RcPaint    rect
	FRestore   int32
	FIncUpdate int32
	Reserved   [32]byte
}

// ---------- Procs ----------

var (
	procRegisterClassExW              = user32DLL().NewProc("RegisterClassExW")
	procCreateWindowExW               = user32DLL().NewProc("CreateWindowExW")
	procDefWindowProcW                = user32DLL().NewProc("DefWindowProcW")
	procDestroyWindow                 = user32DLL().NewProc("DestroyWindow")
	procGetMessageW                   = user32DLL().NewProc("GetMessageW")
	procTranslateMessage              = user32DLL().NewProc("TranslateMessage")
	procDispatchMessageW              = user32DLL().NewProc("DispatchMessageW")
	procSetWindowPos                  = user32DLL().NewProc("SetWindowPos")
	procSetLayeredWindowAttributes    = user32DLL().NewProc("SetLayeredWindowAttributes")
	procCreateRoundRectRgn            = gdi32DLL().NewProc("CreateRoundRectRgn")
	procSetWindowRgn                  = user32DLL().NewProc("SetWindowRgn")
	procDeleteObject                  = gdi32DLL().NewProc("DeleteObject")
	procSystemParametersInfoW         = user32DLL().NewProc("SystemParametersInfoW")
	procBeginPaint                    = user32DLL().NewProc("BeginPaint")
	procEndPaint                      = user32DLL().NewProc("EndPaint")
	procFillRect                      = user32DLL().NewProc("FillRect")
	procDrawTextW                     = user32DLL().NewProc("DrawTextW")
	procSetTextColor                  = gdi32DLL().NewProc("SetTextColor")
	procSetBkMode                     = gdi32DLL().NewProc("SetBkMode")
	procCreateSolidBrush              = gdi32DLL().NewProc("CreateSolidBrush")
	procSetTimer                      = user32DLL().NewProc("SetTimer")
	procKillTimer                     = user32DLL().NewProc("KillTimer")
	procPostQuitMessage               = user32DLL().NewProc("PostQuitMessage")
	procGetClientRect                 = user32DLL().NewProc("GetClientRect")
	procLoadCursorW                   = user32DLL().NewProc("LoadCursorW")
	procSetCursor                     = user32DLL().NewProc("SetCursor")
	procGetModuleHandleW              = kernel32DLL().NewProc("GetModuleHandleW")
	procGradientFill                  = msimg32DLL().NewProc("GradientFill")
	procGdiGradientFill               = gdi32DLL().NewProc("GdiGradientFill")
	procSetProcessDpiAwarenessContext = user32DLL().NewProc("SetProcessDpiAwarenessContext")
	procSetThreadDpiAwarenessContext  = user32DLL().NewProc("SetThreadDpiAwarenessContext")
)

func user32DLL() *windows.LazyDLL   { return windows.NewLazySystemDLL("user32.dll") }
func gdi32DLL() *windows.LazyDLL    { return windows.NewLazySystemDLL("gdi32.dll") }
func msimg32DLL() *windows.LazyDLL  { return windows.NewLazySystemDLL("msimg32.dll") }
func kernel32DLL() *windows.LazyDLL { return windows.NewLazySystemDLL("kernel32.dll") }

// ---------- 状态 ----------

var (
	queueMu       sync.Mutex
	queue         []popupItem
	showing       bool
	classMu       sync.Mutex
	classOK       bool
	classInstance uintptr
)

type popupItem struct {
	content string
}

// ---------- Public API ----------

func show(content string) {
	if content == "" {
		content = "(空提醒)"
	}
	item := popupItem{content: content}

	queueMu.Lock()
	queue = append(queue, item)
	needStart := !showing
	showing = true
	queueMu.Unlock()

	if needStart {
		go popupPump()
	}
}

func shutdown() {
	queueMu.Lock()
	showing = false
	queue = queue[:0]
	queueMu.Unlock()
}

func queueLength() int {
	queueMu.Lock()
	defer queueMu.Unlock()
	return len(queue)
}

// ---------- Pump ----------

func popupPump() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("popup: popupPump panic recovered: %v", r)
		}
		queueMu.Lock()
		showing = false
		queueMu.Unlock()
	}()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	for {
		queueMu.Lock()
		if len(queue) == 0 {
			showing = false
			queueMu.Unlock()
			return
		}
		item := queue[0]
		queue = queue[1:]
		queueMu.Unlock()

		runPopupWindow(item)
		time.Sleep(popupGap)
	}
}

func registerClass() (uintptr, error) {
	classMu.Lock()
	defer classMu.Unlock()
	if classOK {
		return classInstance, nil
	}
	className, _ := windows.UTF16PtrFromString(popupClassName)
	hInstance, _, _ := procGetModuleHandleW.Call(0)
	if hInstance == 0 {
		return 0, fmt.Errorf("获取进程模块句柄失败")
	}

	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		Style:         0,
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     hInstance,
		HbrBackground: 0,
		LpszClassName: className,
	}
	ret, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if ret == 0 && err != syscall.Errno(1410) { // ERROR_CLASS_ALREADY_EXISTS
		return 0, fmt.Errorf("RegisterClassExW 失败: %v", err)
	}
	classOK = true
	classInstance = hInstance
	return classInstance, nil
}

// ---------- WindowProc ----------

var lastPopupContent string // wndProc 拿不到 item 引用，用全局变量传 content（pump 串行，互斥）

func wndProc(hwnd, uMsg, wParam, lParam uintptr) uintptr {
	switch uMsg {
	case WM_PAINT:
		onPaint(hwnd, lastPopupContent)
		return 0
	case WM_LBUTTONDOWN:
		x := int16(lParam & 0xFFFF)
		y := int16((lParam >> 16) & 0xFFFF)
		if isCloseHit(int32(x), int32(y)) {
			procPostQuitMessage.Call(0)
		}
		return 0
	case WM_MOUSEMOVE:
		x := int16(lParam & 0xFFFF)
		y := int16((lParam >> 16) & 0xFFFF)
		if isCloseHit(int32(x), int32(y)) {
			hc, _, _ := procLoadCursorW.Call(0, uintptr(IDC_HAND))
			if hc != 0 {
				procSetCursor.Call(hc)
			}
		}
		return 0
	case WM_TIMER:
		if wParam == timerClose {
			procPostQuitMessage.Call(0)
		}
		return 0
	case WM_DESTROY:
		procKillTimer.Call(hwnd, uintptr(timerClose))
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return ret
}

// ---------- 渲染 ----------

func onPaint(hwnd uintptr, content string) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	var rc rect
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))

	// 主题色
	colBg, colAccent, colTitle, colBody, colClose :=
		colBgLight, colAccentLight, colTitleLight, colBodyLight, colCloseLight
	if isSystemDark() {
		colBg, colAccent, colTitle, colBody, colClose =
			colBgDark, colAccentDark, colTitleDark, colBodyDark, colCloseDark
	}

	// 1) 背景
	bg, _, _ := procCreateSolidBrush.Call(uintptr(colBg))
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), bg)
	procDeleteObject.Call(bg)

	// 2) 左侧 6px accent 条（垂直渐变：上原色下暗化，比纯色更有质感）
	drawAccentGradient(hdc, rc.Left, rc.Top, rc.Left+6, rc.Bottom, uint32(colAccent))

	procSetBkMode.Call(hdc, uintptr(TRANSPARENT))

	// 3) 标题（"Kairo · 便笺提醒"）
	title := "Kairo · 便笺提醒"
	titlePtr, _ := windows.UTF16PtrFromString(title)
	procSetTextColor.Call(hdc, uintptr(colTitle))
	titleRect := rect{
		Left:   rc.Left + 18,
		Top:    rc.Top + 12,
		Right:  rc.Right - 36,
		Bottom: rc.Top + 36,
	}
	procDrawTextW.Call(
		hdc,
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(len([]rune(title))),
		uintptr(unsafe.Pointer(&titleRect)),
		DT_SINGLELINE|DT_NOPREFIX,
	)

	// 4) 关闭按钮 ×
	xMark := "×"
	xPtr, _ := windows.UTF16PtrFromString(xMark)
	procSetTextColor.Call(hdc, uintptr(colClose))
	xRect := rect{
		Left:   rc.Right - 30,
		Top:    rc.Top + 2,
		Right:  rc.Right - 4,
		Bottom: rc.Top + 30,
	}
	procDrawTextW.Call(
		hdc,
		uintptr(unsafe.Pointer(xPtr)),
		1,
		uintptr(unsafe.Pointer(&xRect)),
		DT_SINGLELINE|DT_NOPREFIX,
	)

	// 5) 内容（自动换行，超出省略号截断）
	procSetTextColor.Call(hdc, uintptr(colBody))
	contentPtr, _ := windows.UTF16PtrFromString(content)
	contentRect := rect{
		Left:   rc.Left + 18,
		Top:    rc.Top + 44,
		Right:  rc.Right - 18,
		Bottom: rc.Bottom - 14,
	}
	procDrawTextW.Call(
		hdc,
		uintptr(unsafe.Pointer(contentPtr)),
		uintptr(lenRunes(content)),
		uintptr(unsafe.Pointer(&contentRect)),
		DT_WORDBREAK|DT_NOPREFIX|DT_END_ELLIPSIS,
	)
}

// drawAccentGradient 用 GDI GradientFill 在指定矩形画垂直渐变 accent 条。
func drawAccentGradient(hdc uintptr, x1, y1, x2, y2 int32, colAccent uint32) {
	type triVertex struct {
		X, Y                    int32
		Red, Green, Blue, Alpha uint16
	}
	type gradientRect struct {
		UpperLeft, LowerRight uint32
	}

	r1 := uint16((colAccent >> 16) & 0xFF)
	g1 := uint16((colAccent >> 8) & 0xFF)
	b1 := uint16(colAccent & 0xFF)
	// 下端：暗化 30%（×0.7）
	r2 := uint16((r1 * 7) / 10)
	g2 := uint16((g1 * 7) / 10)
	b2 := uint16((b1 * 7) / 10)

	verts := [2]triVertex{
		{X: x1, Y: y1, Red: r1 << 8, Green: g1 << 8, Blue: b1 << 8, Alpha: 0xFF00},
		{X: x2, Y: y2, Red: r2 << 8, Green: g2 << 8, Blue: b2 << 8, Alpha: 0xFF00},
	}
	gRect := gradientRect{UpperLeft: 0, LowerRight: 1}

	// GRADIENT_FILL_RECT_V = 0x00000001
	if procGradientFill.Find() == nil {
		procGradientFill.Call(
			hdc,
			uintptr(unsafe.Pointer(&verts[0])),
			2,
			uintptr(unsafe.Pointer(&gRect)),
			1,
			0x00000001,
		)
		return
	}
	if procGdiGradientFill.Find() == nil {
		procGdiGradientFill.Call(
			hdc,
			uintptr(unsafe.Pointer(&verts[0])),
			2,
			uintptr(unsafe.Pointer(&gRect)),
			1,
			0x00000001,
		)
		return
	}
	rc := rect{Left: x1, Top: y1, Right: x2, Bottom: y2}
	b, _, _ := procCreateSolidBrush.Call(uintptr(colAccent))
	if b != 0 {
		procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), b)
		procDeleteObject.Call(b)
	}
}

func isCloseHit(x, y int32) bool {
	return x >= popupW-32 && x <= popupW-4 && y >= 2 && y <= 32
}

// ---------- 窗口生命周期 ----------

func runPopupWindow(item popupItem) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("popup: runPopupWindow panic recovered: %v", r)
		}
	}()
	// Win10 下确保 DPI 感知，避免坐标被缩放导致窗口飞出屏幕
	// -2 = DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE, -4 = PER_MONITOR_AWARE_V2
	if procSetThreadDpiAwarenessContext.Find() == nil {
		procSetThreadDpiAwarenessContext.Call(uintptr(^uintptr(3)))
	}
	if procSetProcessDpiAwarenessContext.Find() == nil {
		procSetProcessDpiAwarenessContext.Call(uintptr(^uintptr(3)))
	}
	hInstance, err := registerClass()
	if err != nil {
		log.Printf("popup: registerClass 失败: %v", err)
		return
	}
	lastPopupContent = item.content
	log.Printf("popup: 准备显示提醒，内容长度 %d", lenRunes(item.content))

	// 工作区（避开任务栏）— Win10 下任务栏可能在四边，需正确计算
	var wa rect
	procSystemParametersInfoW.Call(SPI_GETWORKAREA, 0, uintptr(unsafe.Pointer(&wa)), 0)
	// 兜底：若获取失败或工作区为 0，则使用常见分辨率回退，避免窗口飞到负坐标不可见
	if wa.Right <= wa.Left || wa.Bottom <= wa.Top || wa.Right < 800 {
		wa = rect{Left: 0, Top: 0, Right: 1920, Bottom: 1080}
		log.Printf("popup: 工作区异常回退到 1920x1080")
	}
	x := wa.Right - int32(popupW) - popupMargin
	y := wa.Bottom - int32(popupH) - popupMargin
	// 再次钳制，确保窗口完全在工作区内（多显示器/缩放场景）
	if x < wa.Left+8 {
		x = wa.Left + 8
	}
	if y < wa.Top+8 {
		y = wa.Top + 8
	}
	log.Printf("popup: 工作区 %d,%d - %d,%d，窗口位置 %d,%d", wa.Left, wa.Top, wa.Right, wa.Bottom, x, y)

	className, _ := windows.UTF16PtrFromString(popupClassName)
	// 窗口名只做调试标识，截断防超长（便笺正文可能上千字）
	winTitle := item.content
	if lenRunes(winTitle) > 64 {
		winTitle = string([]rune(winTitle)[:64])
	}
	winName, _ := windows.UTF16PtrFromString(winTitle)

	hwnd, _, createErr := procCreateWindowExW.Call(
		uintptr(WS_EX_TOPMOST|WS_EX_TOOLWINDOW|WS_EX_LAYERED|WS_EX_NOACTIVATE),
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(winName)),
		uintptr(WS_POPUP),
		uintptr(x), uintptr(y),
		uintptr(popupW), uintptr(popupH),
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		log.Printf("popup: CreateWindowExW 失败: %v", createErr)
		return
	}
	defer procDestroyWindow.Call(hwnd)

	// 圆角区域
	hrgn, _, _ := procCreateRoundRectRgn.Call(0, 0, uintptr(popupW), uintptr(popupH), 16, 16)
	if hrgn != 0 {
		procSetWindowRgn.Call(hwnd, hrgn, 1)
		// hrgn 由系统接管，不再 DeleteObject
	}

	// 整体 alpha（230 / 255 ≈ 90% 不透明）
	procSetLayeredWindowAttributes.Call(hwnd, 0, 230, LWA_ALPHA)

	// 显示（NOACTIVATE：不抢焦点）
	procSetWindowPos.Call(
		hwnd, HWND_TOPMOST, 0, 0, 0, 0,
		SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE|SWP_SHOWWINDOW,
	)

	// 自动关闭 timer
	procSetTimer.Call(hwnd, uintptr(timerClose), uintptr(popupLive/time.Millisecond), 0)

	// 消息循环（窗口关闭 / 超时 PostQuitMessage 后退出）
	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(
			uintptr(unsafe.Pointer(&m)), 0, 0, 0,
		)
		if int32(ret) <= 0 {
			break // 0 = WM_QUIT, -1 = error
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	procKillTimer.Call(hwnd, uintptr(timerClose))
}

// ---------- 系统主题检测 ----------

// isSystemDark 探测系统是否使用深色主题。
//
// Win10 1809+：HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize
//
//	AppsUseLightTheme = 0 → 深色；= 1 → 浅色；不存在 → 浅色（默认）。
//	Win 7 / 早期 Win10：注册表项不存在 → 浅色。
func isSystemDark() bool {
	const key = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	const value = "AppsUseLightTheme"

	var hKey windows.Handle
	err := windows.RegOpenKeyEx(windows.HKEY_CURRENT_USER, windows.StringToUTF16Ptr(key), 0, windows.KEY_READ, &hKey)
	if err != nil {
		return false
	}
	defer windows.RegCloseKey(hKey)

	var val uint32
	var size uint32 = 4
	var typ uint32
	err = windows.RegQueryValueEx(hKey, windows.StringToUTF16Ptr(value), nil, &typ, (*byte)(unsafe.Pointer(&val)), &size)
	if err != nil {
		return false
	}
	return val == 0
}

// ---------- helpers ----------

// lenRunes 返回字符串的 rune 数（DrawTextW 第三个参数是字符数，不是字节数）。
func lenRunes(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
