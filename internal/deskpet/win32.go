//go:build windows

package deskpet

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 本文件只放 Win32 API 声明、常量与结构体，不掺业务逻辑。
// 全部走 golang.org/x/sys/windows 的 LazyDLL，CGO=0，服务于 Windows 10/11 主线。

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
)

var (
	procRegisterClassExW       = user32.NewProc("RegisterClassExW")
	procCreateWindowExW        = user32.NewProc("CreateWindowExW")
	procDefWindowProcW         = user32.NewProc("DefWindowProcW")
	procDestroyWindow          = user32.NewProc("DestroyWindow")
	procGetModuleHandleW       = kernel32.NewProc("GetModuleHandleW")
	procGetMessageW            = user32.NewProc("GetMessageW")
	procTranslateMessage       = user32.NewProc("TranslateMessage")
	procDispatchMessageW       = user32.NewProc("DispatchMessageW")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procPostQuitMessage        = user32.NewProc("PostQuitMessage")
	procShowWindow             = user32.NewProc("ShowWindow")
	procSetWindowPos           = user32.NewProc("SetWindowPos")
	procGetWindowRect          = user32.NewProc("GetWindowRect")
	procGetClientRect          = user32.NewProc("GetClientRect")
	procGetCursorPos           = user32.NewProc("GetCursorPos")
	procSetCapture             = user32.NewProc("SetCapture")
	procReleaseCapture         = user32.NewProc("ReleaseCapture")
	procSystemParametersInfoW  = user32.NewProc("SystemParametersInfoW")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procSetProcessDPIAware     = user32.NewProc("SetProcessDPIAware")
	procValidateRect           = user32.NewProc("ValidateRect")
	procInvalidateRect         = user32.NewProc("InvalidateRect")
	procUpdateLayeredWindow    = user32.NewProc("UpdateLayeredWindow")
	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procGetWindowTextW         = user32.NewProc("GetWindowTextW")
	procSetWindowTextW         = user32.NewProc("SetWindowTextW")
	procSetFocus               = user32.NewProc("SetFocus")
	procSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	procSendMessageW           = user32.NewProc("SendMessageW")

	procCreateCompatibleDC      = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection        = gdi32.NewProc("CreateDIBSection")
	procSelectObject            = gdi32.NewProc("SelectObject")
	procDeleteObject            = gdi32.NewProc("DeleteObject")
	procDeleteDC                = gdi32.NewProc("DeleteDC")
	procStretchDIBits           = gdi32.NewProc("StretchDIBits")
	procCreateFontW             = gdi32.NewProc("CreateFontW")
	procSetBkMode               = gdi32.NewProc("SetBkMode")
	procSetTextColor            = gdi32.NewProc("SetTextColor")
	procTextOutW                = gdi32.NewProc("TextOutW")
	procSetBkColor              = gdi32.NewProc("SetBkColor")
	procCreateSolidBrush        = gdi32.NewProc("CreateSolidBrush")
	procFillRect                = user32.NewProc("FillRect")
	procGetStockObject          = gdi32.NewProc("GetStockObject")
	procDeleteObjectBrush       = gdi32.NewProc("DeleteObject")
	procRoundRect               = gdi32.NewProc("RoundRect")
	procRectangle               = gdi32.NewProc("Rectangle")
	procMoveToEx                = gdi32.NewProc("MoveToEx")
	procLineTo                  = gdi32.NewProc("LineTo")
	procCreatePen               = gdi32.NewProc("CreatePen")
	procSetWindowRgn            = user32.NewProc("SetWindowRgn")
	procCreateRoundRectRgn      = gdi32.NewProc("CreateRoundRectRgn")
	procCreateRectRgn           = gdi32.NewProc("CreateRectRgn")
	procKillTimer               = user32.NewProc("KillTimer")
	procSetTimer                = user32.NewProc("SetTimer")
	procDrawTextW               = user32.NewProc("DrawTextW")
)

// -------- 窗口样式 --------
const (
	wsPopup      = 0x80000000
	wsChild      = 0x40000000
	wsVisible    = 0x10000000
	wsBorder     = 0x00800000
	wsTabstop    = 0x00010000
	wsClipChildren = 0x02000000

	wsExLayered     = 0x00080000
	wsExTopmost     = 0x00000008
	wsExToolwindow  = 0x00000080
	wsExNoactivate  = 0x08000000
	wsExTransparent = 0x00000020

	cwUseDefault = 0x80000000
)

// -------- 消息 --------
const (
	wmPaint         = 0x000F
	wmTimer         = 0x0113
	wmSetfont       = 0x0030
	wmMouseMove     = 0x0200
	wmLButtonDown   = 0x0201
	wmLButtonUp     = 0x0202
	wmMouseWheel    = 0x020A
	wmSetCursor     = 0x0020
	wmCaptureChange = 0x0215
	wmDestroy       = 0x0002
	wmClose         = 0x0010

	// 自定义消息：托盘 Toggle 请求 / 触发重绘。
	wmAppToggle = 0x8000 + 1
	wmAppQuit   = 0x8000 + 2
)

// -------- ShowWindow / SetWindowPos --------
const (
	swHide          = 0
	swShowNoActivate = 4
	swShow          = 5

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpShowWindow   = 0x0040
)

// -------- 系统参数 --------
const spiGetWorkArea = 0x0030

// -------- 分层窗口 --------
const (
	ulwAlpha  = 0x00000002
	acSrcOver = 0x00
	acSrcAlpha = 0x01
)

// -------- GDI --------
const (
	dibRGBColors = 0
	biRGB        = 0
	srcCopy      = 0x00CC0020

	transparent       = 1
	opaque            = 2
	defaultCharset    = 1
	outDefaultPrecis  = 0
	clipDefaultPrecis = 0
	clearTypeQuality  = 5
	antialiasedQuality = 4
	defaultQuality    = 0
	defaultPitch      = 0
	fwNormal          = 400
	fwBold            = 700

	nullBrush = 5
	psSolid   = 0
)

// rgb 构造 COLORREF。
func rgb(r, g, b uint32) uint32 { return r | (g << 8) | (b << 16) }

// -------- 结构体 --------

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
	HIconSm       uintptr
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type point struct {
	X int32
	Y int32
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

func (r rect) width() int32  { return r.Right - r.Left }
func (r rect) height() int32 { return r.Bottom - r.Top }

type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type rgbQuad struct {
	RgbBlue     byte
	RgbGreen    byte
	RgbRed      byte
	RgbReserved byte
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]rgbQuad
}

type blendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

type paintlessBrush struct{} // 占位，实际 FillRect 用 RECT 直接画

// -------- 便捷封装 --------

func utf16Ptr(s string) (*uint16, error) { return windows.UTF16PtrFromString(s) }

func getModuleHandle() uintptr {
	h, _, _ := procGetModuleHandleW.Call(0)
	return h
}

func registerClass(className string, proc uintptr) error {
	name, err := utf16Ptr(className)
	if err != nil {
		return err
	}
	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   proc,
		HInstance:     getModuleHandle(),
		LpszClassName: name,
	}
	ret, _, err2 := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if ret == 0 && err2 != syscall.Errno(1410) { // ERROR_CLASS_ALREADY_EXISTS
		return err2
	}
	return nil
}

func createWindowEx(exStyle, style uint32, className, title string, x, y, w, h int32, parent uintptr) uintptr {
	cname, _ := utf16Ptr(className)
	tname, _ := utf16Ptr(title)
	hwnd, _, _ := procCreateWindowExW.Call(
		uintptr(exStyle),
		uintptr(unsafe.Pointer(cname)),
		uintptr(unsafe.Pointer(tname)),
		uintptr(style),
		uintptr(int32(x)),
		uintptr(int32(y)),
		uintptr(int32(w)),
		uintptr(int32(h)),
		uintptr(parent),
		0,
		getModuleHandle(),
		0,
	)
	return hwnd
}

func defWindowProc(hwnd, msg, w, l uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, w, l)
	return r
}

func destroyWindow(hwnd uintptr) { procDestroyWindow.Call(hwnd) }

func showWindow(hwnd uintptr, cmd int32) { procShowWindow.Call(hwnd, uintptr(cmd)) }

func setWindowPos(hwnd, hwndInsertAfter uintptr, x, y, cx, cy int32, flags uint32) {
	procSetWindowPos.Call(hwnd, hwndInsertAfter, uintptr(x), uintptr(y), uintptr(cx), uintptr(cy), uintptr(flags))
}

func getWindowRect(hwnd uintptr) rect {
	var r rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

func getClientRect(hwnd uintptr) rect {
	var r rect
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

func getCursorPos() point {
	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p
}

func setCapture(hwnd uintptr) { procSetCapture.Call(hwnd) }
func releaseCapture()        { procReleaseCapture.Call() }

func workArea() rect {
	var r rect
	procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0)
	return r
}

func postMessage(hwnd uintptr, msg uint32, w, l uintptr) {
	procPostMessageW.Call(hwnd, uintptr(msg), w, l)
}

func postQuitMessage(code int32) { procPostQuitMessage.Call(uintptr(code)) }

func getMessage(m *msg) int32 {
	ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(m)), 0, 0, 0)
	return int32(ret)
}

func translateMessage(m *msg) { procTranslateMessage.Call(uintptr(unsafe.Pointer(m))) }
func dispatchMessage(m *msg)  { procDispatchMessageW.Call(uintptr(unsafe.Pointer(m))) }

func validateRect(hwnd uintptr) { procValidateRect.Call(hwnd, 0) }

func invalidateRect(hwnd uintptr, erase bool) {
	procInvalidateRect.Call(hwnd, 0, boolToUintptr(erase))
}

func getWindowText(hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func setWindowText(hwnd uintptr, s string) {
	p, _ := utf16Ptr(s)
	procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(p)))
}

func setFocus(hwnd uintptr) { procSetFocus.Call(hwnd) }

func sendMessage(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
	r, _, _ := procSendMessageW.Call(hwnd, uintptr(msg), w, l)
	return r
}

func setForegroundWindow(hwnd uintptr) { procSetForegroundWindow.Call(hwnd) }

func setTimer(hwnd uintptr, id uintptr, ms uint32) {
	procSetTimer.Call(hwnd, id, uintptr(ms), 0)
}

func killTimer(hwnd uintptr, id uintptr) {
	procKillTimer.Call(hwnd, id)
}

// -------- 分层窗口 / DIB --------

func createCompatibleDC(hdc uintptr) uintptr {
	dc, _, _ := procCreateCompatibleDC.Call(hdc)
	return dc
}

func createDIBSection(hdc uintptr, w, h int32) (hbmp uintptr, bits unsafe.Pointer) {
	bmi := bitmapInfo{Header: bitmapInfoHeader{
		BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		BiWidth:       w,
		BiHeight:      -h, // 负高度 = 自顶向下
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: biRGB,
	}}
	var ppv unsafe.Pointer
	hbmp, _, _ = procCreateDIBSection.Call(hdc, uintptr(unsafe.Pointer(&bmi)), dibRGBColors, uintptr(unsafe.Pointer(&ppv)), 0, 0)
	return hbmp, ppv
}

func selectObject(hdc, obj uintptr) uintptr {
	old, _, _ := procSelectObject.Call(hdc, obj)
	return old
}

func deleteObject(obj uintptr) { procDeleteObject.Call(obj) }
func deleteDC(dc uintptr)      { procDeleteDC.Call(dc) }

func updateLayeredWindow(hwnd, hdc uintptr, w, h int32, pptSrc *point, crKey uint32, blend *blendFunction) {
	pptDst := point{X: 0, Y: 0}
	size := struct {
		Cx int32
		Cy int32
	}{w, h}
	procUpdateLayeredWindow.Call(
		hwnd, 0, 0,
		uintptr(unsafe.Pointer(&size)),
		hdc,
		uintptr(unsafe.Pointer(&pptDst)),
		uintptr(crKey),
		uintptr(unsafe.Pointer(blend)),
		ulwAlpha,
	)
	_ = pptSrc
}

func stretchDIBits(hdc uintptr, dx, dy, dw, dh int32, bits unsafe.Pointer, srcW, srcH int32) {
	bmi := bitmapInfo{Header: bitmapInfoHeader{
		BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		BiWidth:       srcW,
		BiHeight:      -srcH,
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: biRGB,
	}}
	procStretchDIBits.Call(
		hdc,
		uintptr(dx), uintptr(dy), uintptr(dw), uintptr(dh),
		0, 0,
		uintptr(srcW), uintptr(srcH),
		uintptr(bits),
		uintptr(unsafe.Pointer(&bmi)),
		dibRGBColors,
		srcCopy,
	)
}

// -------- 文本 / 绘制 --------

func createFont(heightPx int32, bold bool, face string) uintptr {
	weight := uintptr(fwNormal)
	if bold {
		weight = fwBold
	}
	f, _ := utf16Ptr(face)
	hfont, _, _ := procCreateFontW.Call(
		uintptr(heightPx),
		0, 0, 0,
		weight,
		0, 0, 0,
		defaultCharset,
		outDefaultPrecis,
		clipDefaultPrecis,
		clearTypeQuality,
		defaultPitch,
		uintptr(unsafe.Pointer(f)),
	)
	return hfont
}

func setBkMode(hdc uintptr, mode int32) { procSetBkMode.Call(hdc, uintptr(mode)) }

func createPen(width int32, color uint32) uintptr {
	p, _, _ := procCreatePen.Call(psSolid, uintptr(width), uintptr(color))
	return p
}
func setTextColor(hdc uintptr, color uint32) {
	procSetTextColor.Call(hdc, uintptr(color))
}
func setBkColor(hdc uintptr, color uint32) { procSetBkColor.Call(hdc, uintptr(color)) }

func textOutW(hdc uintptr, x, y int32, s string) {
	p, err := utf16Ptr(s)
	if err != nil {
		return
	}
	procTextOutW.Call(hdc, uintptr(x), uintptr(y), uintptr(unsafe.Pointer(p)), uintptr(len([]rune(s))))
}

func createSolidBrush(color uint32) uintptr {
	b, _, _ := procCreateSolidBrush.Call(uintptr(color))
	return b
}

func fillRect(hdc uintptr, r *rect, brush uintptr) {
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(r)), brush)
}

func roundRect(hdc uintptr, r *rect, w, h int32) {
	procRoundRect.Call(hdc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom), uintptr(w), uintptr(h))
}

func rectangle(hdc uintptr, r *rect) {
	procRectangle.Call(hdc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom))
}

func setWindowRgn(hwnd, hrgn uintptr, redraw bool) {
	procSetWindowRgn.Call(hwnd, hrgn, boolToUintptr(redraw))
}

func createRoundRectRgn(left, top, right, bottom, w, h int32) uintptr {
	r, _, _ := procCreateRoundRectRgn.Call(uintptr(left), uintptr(top), uintptr(right), uintptr(bottom), uintptr(w), uintptr(h))
	return r
}

func boolToUintptr(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

func getDC(hwnd uintptr) uintptr {
	dc, _, _ := procGetDC.Call(hwnd)
	return dc
}

func releaseDC(hwnd, hdc uintptr) { procReleaseDC.Call(hwnd, hdc) }
