//go:build windows

package popup

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// popup_windows.go：Windows 提醒弹窗。
//
// 设计要点：
//   - Win10/11 优先走现代系统 Toast（Windows.UI.Notifications），由系统通知中心渲染。
//   - Win7/Win8 或 Toast 不可用时 fallback 到 Shell_NotifyIconW(NIF_INFO) 经典气泡。
//   - 不内嵌 PNG，不自绘大背景图；视觉交给 Windows，避免拖大单 exe 体积。
//   - 多条提醒排队显示，避免同一时间刷屏。

const (
	kairoAppID      = "Kairo.OpsToolbox"
	nativeClassName = "KairoNativeReminderToastClass_v1"

	nativeUID  uint32 = 0x4b52 // KR
	nativeGap         = 800 * time.Millisecond
	legacyLive        = 10 * time.Second
)

const (
	WM_USER = 0x0400

	NIM_ADD        = 0x00000000
	NIM_MODIFY     = 0x00000001
	NIM_DELETE     = 0x00000002
	NIM_SETVERSION = 0x00000004

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004
	NIF_INFO    = 0x00000010

	NOTIFYICON_VERSION_4 = 4

	NIIF_INFO = 0x00000001

	IDI_INFORMATION = 32516
)

const (
	COINIT_APARTMENTTHREADED = 0x2
	CLSCTX_INPROC_SERVER     = 0x1
	VT_LPWSTR                = 31

	S_OK               uintptr = 0x00000000
	S_FALSE            uintptr = 0x00000001
	RPC_E_CHANGED_MODE uint32  = 0x80010106
)

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

type notifyIconData struct {
	CbSize            uint32
	HWnd              uintptr
	UID               uint32
	UFlags            uint32
	UCallbackMessage  uint32
	HIcon             uintptr
	SzTip             [128]uint16
	DwState           uint32
	DwStateMask       uint32
	SzInfo            [256]uint16
	UTimeoutOrVersion uint32
	SzInfoTitle       [64]uint16
	DwInfoFlags       uint32
}

type osVersionInfoExW struct {
	OSVersionInfoSize uint32
	MajorVersion      uint32
	MinorVersion      uint32
	BuildNumber       uint32
	PlatformID        uint32
	CSDVersion        [128]uint16
	ServicePackMajor  uint16
	ServicePackMinor  uint16
	SuiteMask         uint16
	ProductType       byte
	Reserved          byte
}

type propertyKey struct {
	Fmtid windows.GUID
	Pid   uint32
}

type propVariant struct {
	Vt         uint16
	Reserved1  uint16
	Reserved2  uint16
	Reserved3  uint16
	PointerVal uintptr
}

type iUnknown struct {
	LpVtbl *iUnknownVtbl
}

type iUnknownVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

type iShellLinkW struct {
	LpVtbl *iShellLinkWVtbl
}

type iShellLinkWVtbl struct {
	QueryInterface      uintptr
	AddRef              uintptr
	Release             uintptr
	GetPath             uintptr
	GetIDList           uintptr
	SetIDList           uintptr
	GetDescription      uintptr
	SetDescription      uintptr
	GetWorkingDirectory uintptr
	SetWorkingDirectory uintptr
	GetArguments        uintptr
	SetArguments        uintptr
	GetHotkey           uintptr
	SetHotkey           uintptr
	GetShowCmd          uintptr
	SetShowCmd          uintptr
	GetIconLocation     uintptr
	SetIconLocation     uintptr
	SetRelativePath     uintptr
	Resolve             uintptr
	SetPath             uintptr
}

type iPersistFile struct {
	LpVtbl *iPersistFileVtbl
}

type iPersistFileVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	GetClassID     uintptr
	IsDirty        uintptr
	Load           uintptr
	Save           uintptr
	SaveCompleted  uintptr
	GetCurFile     uintptr
}

type iPropertyStore struct {
	LpVtbl *iPropertyStoreVtbl
}

type iPropertyStoreVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	GetCount       uintptr
	GetAt          uintptr
	GetValue       uintptr
	SetValue       uintptr
	Commit         uintptr
}

var (
	procRegisterClassExW = user32DLL().NewProc("RegisterClassExW")
	procCreateWindowExW  = user32DLL().NewProc("CreateWindowExW")
	procDefWindowProcW   = user32DLL().NewProc("DefWindowProcW")
	procDestroyWindow    = user32DLL().NewProc("DestroyWindow")
	procLoadIconW        = user32DLL().NewProc("LoadIconW")
	procGetModuleHandleW = kernel32DLL().NewProc("GetModuleHandleW")
	procShellNotifyIconW = shell32DLL().NewProc("Shell_NotifyIconW")
	procRtlGetVersion    = ntdllDLL().NewProc("RtlGetVersion")

	procCoInitializeEx   = ole32DLL().NewProc("CoInitializeEx")
	procCoUninitialize   = ole32DLL().NewProc("CoUninitialize")
	procCoCreateInstance = ole32DLL().NewProc("CoCreateInstance")
)

func user32DLL() *windows.LazyDLL   { return windows.NewLazySystemDLL("user32.dll") }
func shell32DLL() *windows.LazyDLL  { return windows.NewLazySystemDLL("shell32.dll") }
func kernel32DLL() *windows.LazyDLL { return windows.NewLazySystemDLL("kernel32.dll") }
func ntdllDLL() *windows.LazyDLL    { return windows.NewLazySystemDLL("ntdll.dll") }
func ole32DLL() *windows.LazyDLL    { return windows.NewLazySystemDLL("ole32.dll") }

var (
	queueMu sync.Mutex
	queue   []popupItem
	showing bool
	classOK bool
	hwnd    uintptr // 所有读写 hwnd 必须在 queueMu 内进行

	// pumpCancel 是 popupPump 的 ctx 取消函数。shutdown 可通过它立即杀掉挂起的
	// powershell 子进程与正在等待的 ticker / IO，避免 8s 的 powershell CombinedOutput
	// 阻塞退出路径。
	pumpCancelMu sync.Mutex
	pumpCancel   context.CancelFunc

	toastShortcutOnce sync.Once
	toastShortcutErr  error
)

var (
	clsidShellLink    = windows.GUID{Data1: 0x00021401, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIShellLinkW    = windows.GUID{Data1: 0x000214F9, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIPersistFile   = windows.GUID{Data1: 0x0000010b, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIPropertyStore = windows.GUID{Data1: 0x00000138, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}

	pkeyAppUserModelID = propertyKey{
		Fmtid: windows.GUID{Data1: 0x9F4C2855, Data2: 0x9F79, Data3: 0x4B39, Data4: [8]byte{0xA8, 0xD0, 0xE1, 0xD4, 0x2D, 0xE1, 0xD5, 0xF3}},
		Pid:   5,
	}
)

type popupItem struct {
	content string
}

func show(content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "(空提醒)"
	}

	queueMu.Lock()
	queue = append(queue, popupItem{content: content})
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
	// 快照 + 清零：避免 pump 拿到旧 hwnd 调 NIM_MODIFY 时窗口已经销毁。
	h := hwnd
	hwnd = 0
	queueMu.Unlock()

	pumpCancelMu.Lock()
	cancel := pumpCancel
	pumpCancel = nil
	pumpCancelMu.Unlock()
	if cancel != nil {
		// 杀掉 powershell 子进程 + 任何阻塞中的 CombinedOutput/Ticker。
		cancel()
	}

	if h == 0 {
		return
	}
	// 先从托盘摘除图标，再销毁窗口，避免残留"幽灵"图标。
	nid := notifyIconData{
		CbSize: uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:   h,
		UID:    nativeUID,
	}
	procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))
	procDestroyWindow.Call(h)
}

func queueLength() int {
	queueMu.Lock()
	defer queueMu.Unlock()
	return len(queue)
}

func popupPump() {
	ctx, cancel := context.WithCancel(context.Background())
	pumpCancelMu.Lock()
	pumpCancel = cancel
	pumpCancelMu.Unlock()
	defer func() {
		pumpCancelMu.Lock()
		pumpCancel = nil
		pumpCancelMu.Unlock()
		cancel()
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

		if isWindows10OrNewer() {
			if err := runModernToast(ctx, item.content); err == nil {
				time.Sleep(nativeGap)
				continue
			} else {
				log.Printf("popup: Win10 Toast 失败，回退传统气泡: %v", err)
			}
		}

		if err := registerClass(); err != nil {
			log.Printf("popup: registerClass 失败: %v", err)
			time.Sleep(nativeGap)
			continue
		}
		runLegacyBalloon(item)
		time.Sleep(nativeGap)
	}
}

func isWindows10OrNewer() bool {
	var vi osVersionInfoExW
	vi.OSVersionInfoSize = uint32(unsafe.Sizeof(vi))
	ret, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&vi)))
	if ret != 0 {
		return false
	}
	return vi.MajorVersion >= 10
}

func runModernToast(parentCtx context.Context, content string) error {
	if err := ensureToastShortcutOnce(); err != nil {
		return err
	}

	toastXML := buildToastXML(content)
	xmlB64 := base64.StdEncoding.EncodeToString([]byte(toastXML))
	ps := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
[Windows.UI.Notifications.ToastNotification, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] > $null
$AppId = '%s'
$XmlText = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))
$Xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$Xml.LoadXml($XmlText)
$Toast = [Windows.UI.Notifications.ToastNotification]::new($Xml)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($AppId).Show($Toast)
`, psQuote(kairoAppID), xmlB64)

	// parentCtx 由 popupPump 提供，shutdown 时会 cancel 它 — 由此杀掉 powershell 子进程。
	// 8s 超时是兜底，二者同时有效：先到先杀。
	ctx, cancel := context.WithTimeout(parentCtx, 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile",
		"-ExecutionPolicy", "Bypass",
		"-WindowStyle", "Hidden",
		"-EncodedCommand", utf16LEBase64(ps),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("powershell toast 超时: %w", ctx.Err())
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("powershell toast 执行失败: %s", msg)
	}
	return nil
}

func buildToastXML(content string) string {
	now := time.Now()
	timeStr := fmt.Sprintf("%d:%02d", now.Hour(), now.Minute())
	return `<toast scenario="reminder" launch="action=open">
  <visual>
    <binding template="ToastGeneric">
      <text placement="attribution">Kairo</text>
      <text hint-style="header" hint-wrap="true">便笺提醒</text>
      <text hint-wrap="true" hint-maxLines="3">` + xmlEscape(content) + `</text>
      <text hint-style="captionSubtle" hint-wrap="true">` + timeStr + `</text>
    </binding>
  </visual>
  <audio src="ms-winsoundevent:Notification.Reminder" />
</toast>`
}

func ensureToastShortcutOnce() error {
	toastShortcutOnce.Do(func() {
		toastShortcutErr = ensureToastShortcut()
	})
	return toastShortcutErr
}

func ensureToastShortcut() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取 exe 路径失败: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	appData := os.Getenv("APPDATA")
	if appData == "" {
		return fmt.Errorf("APPDATA 为空，无法创建开始菜单快捷方式")
	}
	shortcutPath := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Kairo.lnk")
	if err := os.MkdirAll(filepath.Dir(shortcutPath), 0o755); err != nil {
		return fmt.Errorf("创建开始菜单目录失败: %w", err)
	}

	coUninit, err := coInitialize()
	if err != nil {
		return err
	}
	if coUninit {
		defer procCoUninitialize.Call()
	}

	var shellLink *iShellLinkW
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)),
		0,
		uintptr(CLSCTX_INPROC_SERVER),
		uintptr(unsafe.Pointer(&iidIShellLinkW)),
		uintptr(unsafe.Pointer(&shellLink)),
	)
	if failed(hr) || shellLink == nil {
		return hresultError("CoCreateInstance(IShellLinkW)", hr)
	}
	defer releaseCOM(unsafe.Pointer(shellLink))

	if err := shellLink.SetPath(exePath); err != nil {
		return err
	}
	if err := shellLink.SetWorkingDirectory(filepath.Dir(exePath)); err != nil {
		return err
	}
	if err := shellLink.SetIconLocation(exePath, 0); err != nil {
		return err
	}
	if err := shellLink.SetDescription("Kairo Ops Toolbox"); err != nil {
		return err
	}
	if err := setShellLinkAppID(shellLink, kairoAppID); err != nil {
		return err
	}
	if err := saveShellLink(shellLink, shortcutPath); err != nil {
		return err
	}
	return nil
}

func coInitialize() (bool, error) {
	hr, _, _ := procCoInitializeEx.Call(0, uintptr(COINIT_APARTMENTTHREADED))
	if hr == S_OK || hr == S_FALSE {
		return true, nil
	}
	if uint32(hr) == RPC_E_CHANGED_MODE {
		// 当前线程已经用别的模式初始化过 COM，继续使用即可，但不能 CoUninitialize。
		return false, nil
	}
	return false, hresultError("CoInitializeEx", hr)
}

func setShellLinkAppID(shellLink *iShellLinkW, appID string) error {
	psPtr, err := queryInterface(unsafe.Pointer(shellLink), &iidIPropertyStore)
	if err != nil {
		return err
	}
	ps := (*iPropertyStore)(psPtr)
	defer releaseCOM(psPtr)

	appIDPtr, err := windows.UTF16PtrFromString(appID)
	if err != nil {
		return err
	}
	pv := propVariant{Vt: VT_LPWSTR, PointerVal: uintptr(unsafe.Pointer(appIDPtr))}
	hr, _, _ := syscall.SyscallN(
		ps.LpVtbl.SetValue,
		uintptr(unsafe.Pointer(ps)),
		uintptr(unsafe.Pointer(&pkeyAppUserModelID)),
		uintptr(unsafe.Pointer(&pv)),
	)
	if failed(hr) {
		return hresultError("IPropertyStore.SetValue(AppUserModelID)", hr)
	}
	hr, _, _ = syscall.SyscallN(ps.LpVtbl.Commit, uintptr(unsafe.Pointer(ps)))
	if failed(hr) {
		return hresultError("IPropertyStore.Commit", hr)
	}
	return nil
}

func saveShellLink(shellLink *iShellLinkW, shortcutPath string) error {
	pfPtr, err := queryInterface(unsafe.Pointer(shellLink), &iidIPersistFile)
	if err != nil {
		return err
	}
	pf := (*iPersistFile)(pfPtr)
	defer releaseCOM(pfPtr)

	pathPtr, err := windows.UTF16PtrFromString(shortcutPath)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(
		pf.LpVtbl.Save,
		uintptr(unsafe.Pointer(pf)),
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(1),
	)
	if failed(hr) {
		return hresultError("IPersistFile.Save", hr)
	}
	return nil
}

func queryInterface(obj unsafe.Pointer, iid *windows.GUID) (unsafe.Pointer, error) {
	if obj == nil {
		return nil, fmt.Errorf("QueryInterface: nil object")
	}
	var out unsafe.Pointer
	hr, _, _ := syscall.SyscallN(
		(*iUnknown)(obj).LpVtbl.QueryInterface,
		uintptr(obj),
		uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&out)),
	)
	if failed(hr) || out == nil {
		return nil, hresultError("QueryInterface", hr)
	}
	return out, nil
}

func releaseCOM(obj unsafe.Pointer) {
	if obj != nil {
		syscall.SyscallN((*iUnknown)(obj).LpVtbl.Release, uintptr(obj))
	}
}

func registerClass() error {
	if classOK {
		return nil
	}

	className, _ := windows.UTF16PtrFromString(nativeClassName)
	hInstance, _, _ := procGetModuleHandleW.Call(0)

	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   syscall.NewCallback(nativeWndProc),
		HInstance:     hInstance,
		HbrBackground: 0,
		LpszClassName: className,
	}
	ret, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if ret == 0 && err != syscall.Errno(1410) { // ERROR_CLASS_ALREADY_EXISTS
		return fmt.Errorf("RegisterClassExW 失败: %v", err)
	}
	classOK = true
	return nil
}

func nativeWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return ret
}

func runLegacyBalloon(item popupItem) {
	// hwnd 的所有读写必须在 queueMu 内进行：shutdown 在锁内把 hwnd 清零，
	// 此处在锁内快照出本地 h 之后再去用，避免与 DestroyWindow 并发。
	queueMu.Lock()
	var h uintptr
	if hwnd == 0 {
		h = createHiddenWindow()
		if h == 0 {
			queueMu.Unlock()
			log.Printf("popup: CreateWindowExW 失败")
			return
		}

		hIcon, _, _ := procLoadIconW.Call(0, uintptr(IDI_INFORMATION))
		nid := notifyIconData{
			CbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
			HWnd:             h,
			UID:              nativeUID,
			UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
			UCallbackMessage: WM_USER + 77,
			HIcon:            hIcon,
		}
		copyUTF16(nid.SzTip[:], "Kairo")

		ret, _, err := procShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&nid)))
		if ret == 0 {
			log.Printf("popup: Shell_NotifyIconW(NIM_ADD) 失败: %v", err)
			procDestroyWindow.Call(h)
			queueMu.Unlock()
			return
		}

		nid.UTimeoutOrVersion = NOTIFYICON_VERSION_4
		ret, _, err = procShellNotifyIconW.Call(NIM_SETVERSION, uintptr(unsafe.Pointer(&nid)))
		if ret == 0 {
			log.Printf("popup: Shell_NotifyIconW(NIM_SETVERSION) 失败: %v", err)
		}
		// NIM_ADD 成功后才把 hwnd 登记进全局变量。
		hwnd = h
	} else {
		h = hwnd
	}
	queueMu.Unlock()

	nid := notifyIconData{
		CbSize:      uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:        h,
		UID:         nativeUID,
		UFlags:      NIF_INFO,
		DwInfoFlags: NIIF_INFO,
	}
	copyUTF16(nid.SzInfoTitle[:], "Kairo · 便笺提醒")
	copyUTF16(nid.SzInfo[:], item.content)

	ret, _, err := procShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&nid)))
	if ret == 0 {
		log.Printf("popup: Shell_NotifyIconW(NIM_MODIFY) 失败: %v", err)
	}
}

func createHiddenWindow() uintptr {
	className, _ := windows.UTF16PtrFromString(nativeClassName)
	winName, _ := windows.UTF16PtrFromString("KairoReminderNotification")
	hInstance, _, _ := procGetModuleHandleW.Call(0)
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(winName)),
		0,
		0, 0, 0, 0,
		0, 0, hInstance, 0,
	)
	return hwnd
}

func (sl *iShellLinkW) SetPath(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(sl.LpVtbl.SetPath, uintptr(unsafe.Pointer(sl)), uintptr(unsafe.Pointer(p)))
	if failed(hr) {
		return hresultError("IShellLinkW.SetPath", hr)
	}
	return nil
}

func (sl *iShellLinkW) SetWorkingDirectory(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(sl.LpVtbl.SetWorkingDirectory, uintptr(unsafe.Pointer(sl)), uintptr(unsafe.Pointer(p)))
	if failed(hr) {
		return hresultError("IShellLinkW.SetWorkingDirectory", hr)
	}
	return nil
}

func (sl *iShellLinkW) SetIconLocation(path string, index int) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(sl.LpVtbl.SetIconLocation, uintptr(unsafe.Pointer(sl)), uintptr(unsafe.Pointer(p)), uintptr(index))
	if failed(hr) {
		return hresultError("IShellLinkW.SetIconLocation", hr)
	}
	return nil
}

func (sl *iShellLinkW) SetDescription(desc string) error {
	p, err := windows.UTF16PtrFromString(desc)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(sl.LpVtbl.SetDescription, uintptr(unsafe.Pointer(sl)), uintptr(unsafe.Pointer(p)))
	if failed(hr) {
		return hresultError("IShellLinkW.SetDescription", hr)
	}
	return nil
}

func failed(hr uintptr) bool {
	return int32(uint32(hr)) < 0
}

func hresultError(action string, hr uintptr) error {
	return fmt.Errorf("%s failed: HRESULT 0x%08x", action, uint32(hr))
}

func copyUTF16(dst []uint16, s string) {
	if len(dst) == 0 {
		return
	}
	u := windows.StringToUTF16(s)
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

func utf16LEBase64(s string) string {
	u16 := utf16.Encode([]rune(s))
	buf := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(buf[i*2:], v)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func xmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	).Replace(s)
}

func psQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
