//go:build windows

package httpserver

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

type winGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	ole32Picker                     = syscall.NewLazyDLL("ole32.dll")
	shell32Picker                   = syscall.NewLazyDLL("shell32.dll")
	user32Picker                    = syscall.NewLazyDLL("user32.dll")
	procCoCreateInstance            = ole32Picker.NewProc("CoCreateInstance")
	procCoTaskMemFree               = ole32Picker.NewProc("CoTaskMemFree")
	procOleInitialize               = ole32Picker.NewProc("OleInitialize")
	procOleUninitialize             = ole32Picker.NewProc("OleUninitialize")
	procSHCreateItemFromParsingName = shell32Picker.NewProc("SHCreateItemFromParsingName")
	procSHBrowseForFolderW          = shell32Picker.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW        = shell32Picker.NewProc("SHGetPathFromIDListW")
	procGetForegroundWindow         = user32Picker.NewProc("GetForegroundWindow")
	procGetAncestor                 = user32Picker.NewProc("GetAncestor")
	procSetForegroundWindow         = user32Picker.NewProc("SetForegroundWindow")
	procSendMessageW                = user32Picker.NewProc("SendMessageW")
	procIsWindowVisible             = user32Picker.NewProc("IsWindowVisible")
	procGetWindowTextW              = user32Picker.NewProc("GetWindowTextW")
	procGetWindowTextLengthW        = user32Picker.NewProc("GetWindowTextLengthW")
	procGetClassNameW               = user32Picker.NewProc("GetClassNameW")
	procGetWindowRect               = user32Picker.NewProc("GetWindowRect")

	clsidFileOpenDialog = winGUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidIFileOpenDialog  = winGUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidIShellItem       = winGUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

const (
	clsctxInprocServer          = 0x1
	gaRoot                      = 2
	fosPickFolders              = 0x20
	fosForceFileSystem          = 0x40
	fosPathMustExist            = 0x800
	fosFileMustExist            = 0x1000
	fosNoChangeDir              = 0x8
	sigdnFileSysPath            = 0x80058000
	hresultCancel               = 0x800704C7
	bifReturnOnlyFileSystemDirs = 0x1
	bifNewDialogStyle           = 0x40
	bffmInitialized             = 0x400 + 1
	bffmSetSelectionW           = 0x400 + 103
)

type browseInfo struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

type iFileOpenDialogVtbl struct {
	QueryInterface      uintptr
	AddRef              uintptr
	Release             uintptr
	Show                uintptr
	SetFileTypes        uintptr
	SetFileTypeIndex    uintptr
	GetFileTypeIndex    uintptr
	Advise              uintptr
	Unadvise            uintptr
	SetOptions          uintptr
	GetOptions          uintptr
	SetDefaultFolder    uintptr
	SetFolder           uintptr
	GetFolder           uintptr
	GetCurrentSelection uintptr
	SetFileName         uintptr
	GetFileName         uintptr
	SetTitle            uintptr
	GetTitle            uintptr
	SetOkButtonLabel    uintptr
	SetFileNameLabel    uintptr
	GetResult           uintptr
	AddPlace            uintptr
	SetDefaultExtension uintptr
	Close               uintptr
	SetClientGuid       uintptr
	ClearClientData     uintptr
	SetFilter           uintptr
	GetResults          uintptr
	GetSelectedItems    uintptr
}

type iFileOpenDialog struct {
	vtbl *iFileOpenDialogVtbl
}

type iShellItemVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	BindToHandler  uintptr
	GetParent      uintptr
	GetDisplayName uintptr
	GetAttributes  uintptr
	Compare        uintptr
}

type iShellItem struct {
	vtbl *iShellItemVtbl
}

func chooseFile() (string, error) { return chooseFileAt("") }
func chooseDir() (string, error)  { return chooseDirAt("") }

func chooseFileAt(initial string) (string, error) {
	return invokePicker(func() (string, error) { return pickWindowsPath(false, initial) })
}

func chooseDirAt(initial string) (string, error) {
	// 目录选择使用 Shell 的 BROWSEFORFOLDER 对话框。它仍然是系统原生、
	// 可由 owner 窗口定位的对话框，但不需要在每次调用中手动创建/释放
	// IFileOpenDialog 与 IShellItem，避免 RichEdit/COM 宿主在第二次选择后
	// 进入已释放的 apartment 状态（问题 5/19）。
	return invokePicker(func() (string, error) { return pickWindowsDirectory(initial) })
}

type winRect struct {
	Left, Top, Right, Bottom int32
}

func windowTitle(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+2)
	got, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if got == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func windowClass(hwnd uintptr) string {
	buf := make([]uint16, 256)
	got, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if got == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:got])
}

func isVisibleTopWindow(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	vis, _, _ := procIsWindowVisible.Call(hwnd)
	return vis != 0
}

func windowArea(hwnd uintptr) int {
	var r winRect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	w := int(r.Right - r.Left)
	h := int(r.Bottom - r.Top)
	if w < 0 || h < 0 {
		return 0
	}
	return w * h
}

func normalizeOwnerHWND(hwnd uintptr) uintptr {
	if hwnd == 0 {
		return 0
	}
	root, _, _ := procGetAncestor.Call(hwnd, gaRoot)
	if root != 0 {
		return root
	}
	return hwnd
}

func pickerOwnerHWND() uintptr {
	fg := normalizeOwnerHWND(func() uintptr {
		hwnd, _, _ := procGetForegroundWindow.Call()
		return hwnd
	}())
	if fg == 0 || !isVisibleTopWindow(fg) {
		return 0
	}
	if !usablePickerOwner(windowClass(fg), windowTitle(fg), windowArea(fg)) {
		return 0
	}
	return fg
}

func browseFolderCallback(hwnd, message, wParam, lParam uintptr) uintptr {
	if uint32(message) == bffmInitialized && lParam != 0 {
		// BFFM_SETSELECTIONW 的 lParam 是仍存活的绝对路径指针；
		// pickWindowsDirectory 会一直阻塞到对话框关闭，因此指针有效。
		procSendMessageW.Call(hwnd, bffmSetSelectionW, 1, lParam)
	}
	return 0
}

var browseFolderCallbackPtr = syscall.NewCallback(browseFolderCallback)

func pickWindowsDirectory(initial string) (path string, err error) {
	pickerMu.Lock()
	defer pickerMu.Unlock()
	defer func() {
		if rv := recover(); rv != nil {
			err = fmt.Errorf("文件夹选择器异常: %v", rv)
		}
	}()

	start := pickerStartDir(initial)
	var startPtr *uint16
	if start != "" {
		startPtr, err = syscall.UTF16PtrFromString(start)
		if err != nil {
			return "", fmt.Errorf("准备文件夹选择路径失败: %w", err)
		}
	}
	title, _ := syscall.UTF16PtrFromString("选择文件夹")
	nameBuf := make([]uint16, 32768)
	info := browseInfo{
		hwndOwner:      pickerOwnerHWND(),
		pszDisplayName: &nameBuf[0],
		lpszTitle:      title,
		ulFlags:        bifReturnOnlyFileSystemDirs | bifNewDialogStyle,
		lpfn:           browseFolderCallbackPtr,
		lParam:         uintptr(unsafe.Pointer(startPtr)),
	}
	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&info)))
	if pidl == 0 {
		return "", nil // 用户取消
	}
	defer procCoTaskMemFree.Call(pidl)
	pathBuf := make([]uint16, 32768)
	ok, _, _ := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&pathBuf[0])))
	if ok == 0 {
		return "", fmt.Errorf("读取所选文件夹失败")
	}
	path = syscall.UTF16ToString(pathBuf)
	if path == "" {
		return "", nil
	}
	rememberPicked(path)
	return path, nil
}

func pickWindowsPath(directory bool, initial string) (out string, err error) {
	pickerMu.Lock()
	defer pickerMu.Unlock()
	defer func() {
		if rv := recover(); rv != nil {
			err = fmt.Errorf("pickWindowsPath panic: %v", rv)
		}
	}()

	// 线程模型：若已通过 winui host 的 UI 线程进入（uiInvoke != nil），则当前
	// 已是 STA 且已 LockOSThread，无需再次 Lock/OleInitialize，避免破坏 host 的 COM 套间。
	// 仅当无 host（单测/非 Windows）时，才在当前 goroutine 上初始化 COM。
	var hr uintptr
	needLock := uiInvoke == nil
	if needLock {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		h, _, _ := procOleInitialize.Call(0)
		hr = h
		if hr != 0 && hr != 1 {
			return "", fmt.Errorf("OleInitialize 失败: 0x%x", hr)
		}
		// 非 host 路径下，对应的 OleUninitialize 由 defer 在 Unlock 之前调用
		// 但为避免与 host 的“永不反初始化”策略冲突，仅在 needLock 时才	defer Uninitialize
		defer procOleUninitialize.Call()
	}
	// host 线程在 winui.run 中已经初始化了 OLE，并且会一直保持到消息循环结束。
	// 这里不要再次调用 OleInitialize/OleUninitialize，避免二次对话框关闭时
	// Shell/RichEdit 观察到 COM apartment 引用计数变化。

	var dlg *iFileOpenDialog
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)),
		uintptr(unsafe.Pointer(&dlg)),
	)
	if hr != 0 || dlg == nil {
		return "", fmt.Errorf("无法创建系统文件对话框: 0x%x", hr)
	}
	defer dlg.release()

	options := uintptr(fosForceFileSystem | fosPathMustExist | fosNoChangeDir)
	if directory {
		options |= fosPickFolders
	} else {
		options |= fosFileMustExist
	}
	if r := dlg.setOptions(options); r != 0 {
		return "", fmt.Errorf("设置对话框选项失败: 0x%x", r)
	}

	title := "选择文件"
	if directory {
		title = "选择文件夹"
	}
	if r := dlg.setTitle(title); r != 0 {
		return "", fmt.Errorf("设置对话框标题失败: 0x%x", r)
	}

	if start := pickerStartDir(initial); start != "" {
		if item, err := shCreateItem(start); err == nil {
			_ = dlg.setDefaultFolder(item)
			_ = dlg.setFolder(item)
			item.release()
		}
	}

	owner := pickerOwnerHWND()
	if owner != 0 {
		_, _, _ = procSetForegroundWindow.Call(owner)
	}
	hr = dlg.show(owner)
	if uint32(hr) == hresultCancel {
		return "", nil
	}
	if hr != 0 {
		return "", fmt.Errorf("打开系统对话框失败: 0x%x", hr)
	}

	item, getHR := dlg.result()
	if getHR != 0 || item == nil {
		return "", fmt.Errorf("读取所选路径失败: 0x%x", getHR)
	}
	defer item.release()
	path, err := item.filePath()
	if err != nil {
		return "", err
	}
	rememberPicked(path)
	return path, nil
}

func (d *iFileOpenDialog) show(hwnd uintptr) uintptr {
	r, _, _ := syscall.SyscallN(d.vtbl.Show, uintptr(unsafe.Pointer(d)), hwnd)
	return r
}

func (d *iFileOpenDialog) setOptions(options uintptr) uintptr {
	r, _, _ := syscall.SyscallN(d.vtbl.SetOptions, uintptr(unsafe.Pointer(d)), options)
	return r
}

func (d *iFileOpenDialog) setTitle(title string) uintptr {
	ptr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0x80070057
	}
	r, _, _ := syscall.SyscallN(d.vtbl.SetTitle, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(ptr)))
	return r
}

func (d *iFileOpenDialog) setDefaultFolder(item *iShellItem) uintptr {
	r, _, _ := syscall.SyscallN(d.vtbl.SetDefaultFolder, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(item)))
	return r
}

func (d *iFileOpenDialog) setFolder(item *iShellItem) uintptr {
	r, _, _ := syscall.SyscallN(d.vtbl.SetFolder, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(item)))
	return r
}

func (d *iFileOpenDialog) result() (*iShellItem, uintptr) {
	var item *iShellItem
	r, _, _ := syscall.SyscallN(d.vtbl.GetResult, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(&item)))
	return item, r
}

func (d *iFileOpenDialog) release() {
	_, _, _ = syscall.SyscallN(d.vtbl.Release, uintptr(unsafe.Pointer(d)))
}

func (s *iShellItem) filePath() (string, error) {
	var name *uint16
	r, _, _ := syscall.SyscallN(s.vtbl.GetDisplayName, uintptr(unsafe.Pointer(s)), uintptr(sigdnFileSysPath), uintptr(unsafe.Pointer(&name)))
	if r != 0 || name == nil {
		return "", fmt.Errorf("读取文件路径失败: 0x%x", r)
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(name)))
	return syscall.UTF16ToString((*[32768]uint16)(unsafe.Pointer(name))[:]), nil
}

func (s *iShellItem) release() {
	_, _, _ = syscall.SyscallN(s.vtbl.Release, uintptr(unsafe.Pointer(s)))
}

func shCreateItem(path string) (*iShellItem, error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var item *iShellItem
	hr, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(ptr)),
		0,
		uintptr(unsafe.Pointer(&iidIShellItem)),
		uintptr(unsafe.Pointer(&item)),
	)
	if hr != 0 || item == nil {
		return nil, fmt.Errorf("SHCreateItemFromParsingName 失败: 0x%x", hr)
	}
	return item, nil
}
