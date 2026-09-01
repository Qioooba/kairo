//go:build windows

package httpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	comdlg32                 = syscall.NewLazyDLL("comdlg32.dll")
	shell32                  = syscall.NewLazyDLL("shell32.dll")
	ole32                    = syscall.NewLazyDLL("ole32.dll")
	user32Picker             = syscall.NewLazyDLL("user32.dll")
	procGetOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
	procCommDlgExtendedError = comdlg32.NewProc("CommDlgExtendedError")
	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
	procOleInitialize        = ole32.NewProc("OleInitialize")
	procOleUninitialize      = ole32.NewProc("OleUninitialize")
	procGetForegroundWindow  = user32Picker.NewProc("GetForegroundWindow")
	procSendMessageW         = user32Picker.NewProc("SendMessageW")
)

const (
	ofnExplorer         = 0x00080000
	ofnFileMustExist    = 0x00001000
	ofnPathMustExist    = 0x00000800
	ofnNoChangeDir      = 0x00000008
	bifReturnOnlyFSDirs = 0x00000001
	bifNewDialogStyle   = 0x00000040
	bifEditBox          = 0x00000010
	bffmInitialized     = 1
	bffmSetSelectionW   = 0x400 + 103
)

type openFileNameW struct {
	structSize, _pad0              uint32
	hwndOwner, hInstance           uintptr
	filter, customFilter           *uint16
	maxCustomFilter, filterIndex   uint32
	file                           *uint16
	maxFile                        uint32
	fileTitle                      *uint16
	maxFileTitle                   uint32
	initialDir, title              *uint16
	flags                          uint32
	fileOffset, fileExtension      uint16
	defaultExt                     *uint16
	customData, hook, templateName uintptr
	reserved                       unsafe.Pointer
	reservedSize, flagsEx          uint32
}

type browseInfoW struct {
	hwndOwner, root    uintptr
	displayName, title *uint16
	flags              uint32
	callback, param    uintptr
	image              int32
}

func pickerOwner() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	return hwnd
}

func pickerInitialDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	desktop := filepath.Join(home, "Desktop")
	if st, err := os.Stat(desktop); err == nil && st.IsDir() {
		return desktop
	}
	return home
}

func chooseFile() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	file := make([]uint16, 32768)
	filter, _ := syscall.UTF16PtrFromString("所有文件 (*.*)\x00*.*\x00")
	title, _ := syscall.UTF16PtrFromString("选择文件")
	initial, _ := syscall.UTF16PtrFromString(pickerInitialDir())
	of := openFileNameW{hwndOwner: pickerOwner(), filter: filter, filterIndex: 1, file: &file[0], maxFile: uint32(len(file)), initialDir: initial, title: title, flags: ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnNoChangeDir}
	of.structSize = uint32(unsafe.Sizeof(of))
	ok, _, callErr := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&of)))
	if ok != 0 {
		return syscall.UTF16ToString(file), nil
	}
	code, _, _ := procCommDlgExtendedError.Call()
	if code == 0 {
		return "", nil
	}
	return "", fmt.Errorf("文件选择失败（系统错误 0x%x）: %v", code, callErr)
}

func chooseDir() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	_, _, _ = procOleInitialize.Call(0)
	defer procOleUninitialize.Call()
	display := make([]uint16, 260)
	title, _ := syscall.UTF16PtrFromString("选择文件夹")
	initial, _ := syscall.UTF16PtrFromString(pickerInitialDir())
	callback := syscall.NewCallback(func(hwnd, msg, _wparam, param uintptr) uintptr {
		if msg == bffmInitialized && param != 0 {
			procSendMessageW.Call(hwnd, bffmSetSelectionW, 1, param)
		}
		return 0
	})
	bi := browseInfoW{hwndOwner: pickerOwner(), displayName: &display[0], title: title, flags: bifReturnOnlyFSDirs | bifNewDialogStyle | bifEditBox, callback: callback, param: uintptr(unsafe.Pointer(initial)), image: -1}
	pidl, _, callErr := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", nil
	}
	defer procCoTaskMemFree.Call(pidl)
	path := make([]uint16, 32768)
	ok, _, _ := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&path[0])))
	if ok == 0 {
		return "", fmt.Errorf("文件夹选择失败: %v", callErr)
	}
	return syscall.UTF16ToString(path), nil
}
