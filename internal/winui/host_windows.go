//go:build windows

package winui

import (
	"errors"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	hostClass     = "KairoNativeHost_v1"
	wmClose       = 0x0010
	wmDestroy     = 0x0002
	wmAppDispatch = 0x8000 + 0x41
)

type hostTask struct {
	fn   func() error
	done chan error
}

type windowsHost struct {
	mu      sync.Mutex
	hwnd    uintptr
	queue   []hostTask
	started bool
	stopped bool
	ready   chan error
	done    chan struct{}
}

var (
	user32                            = windows.NewLazySystemDLL("user32.dll")
	kernel32                          = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")

	hostsMu             sync.RWMutex
	hosts               = map[uintptr]*windowsHost{}
	hostWndProcCallback = syscall.NewCallback(hostWndProc)
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

type message struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// New creates the process-wide style of native host. Start must be called once.
func New() *Host {
	return &Host{nativeState: &windowsHost{ready: make(chan error, 1), done: make(chan struct{})}}
}

func (h *Host) native() *windowsHost { return h.nativeState.(*windowsHost) }

// Start creates a dedicated, per-monitor-DPI-aware UI thread and message loop.
func (h *Host) Start() error {
	n := h.native()
	n.mu.Lock()
	if n.started {
		n.mu.Unlock()
		return errors.New("winui: host already started")
	}
	n.started = true
	n.mu.Unlock()
	go n.run()
	return <-n.ready
}

func (n *windowsHost) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(n.done)

	// PER_MONITOR_AWARE_V2. This target intentionally requires Windows 10.
	procSetProcessDpiAwarenessContext.Call(^uintptr(3))
	instance, _, _ := procGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString(hostClass)
	wc := wndClassEx{CbSize: uint32(unsafe.Sizeof(wndClassEx{})), LpfnWndProc: hostWndProcCallback, HInstance: instance, LpszClassName: className}
	ret, _, regErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if ret == 0 && regErr != syscall.Errno(1410) {
		n.ready <- regErr
		return
	}
	title, _ := windows.UTF16PtrFromString("KairoNativeHost")
	hwnd, _, createErr := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		n.ready <- createErr
		return
	}
	n.mu.Lock()
	n.hwnd = hwnd
	n.mu.Unlock()
	hostsMu.Lock()
	hosts[hwnd] = n
	hostsMu.Unlock()
	n.ready <- nil

	var msg message
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	hostsMu.Lock()
	delete(hosts, hwnd)
	hostsMu.Unlock()
	n.mu.Lock()
	n.stopped = true
	remaining := n.queue
	n.queue = nil
	n.mu.Unlock()
	for _, task := range remaining {
		if task.done != nil {
			task.done <- errors.New("winui: host stopped")
		}
	}
}

func hostWndProc(hwnd, msg, w, l uintptr) uintptr {
	hostsMu.RLock()
	h := hosts[hwnd]
	hostsMu.RUnlock()
	switch uint32(msg) {
	case wmAppDispatch:
		if h != nil {
			h.drain()
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	default:
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, w, l)
		return r
	}
}

func (n *windowsHost) drain() {
	for {
		n.mu.Lock()
		if len(n.queue) == 0 {
			n.mu.Unlock()
			return
		}
		task := n.queue[0]
		n.queue = n.queue[1:]
		n.mu.Unlock()
		err := task.fn()
		if task.done != nil {
			task.done <- err
		}
	}
}

func (h *Host) enqueue(task hostTask) bool {
	n := h.native()
	n.mu.Lock()
	if !n.started || n.stopped || n.hwnd == 0 {
		n.mu.Unlock()
		return false
	}
	n.queue = append(n.queue, task)
	hwnd := n.hwnd
	n.mu.Unlock()
	procPostMessageW.Call(hwnd, wmAppDispatch, 0, 0)
	return true
}

// Post queues fn on the UI thread.
func (h *Host) Post(fn func()) bool {
	if fn == nil {
		return true
	}
	return h.enqueue(hostTask{fn: func() error { fn(); return nil }})
}

// Invoke queues fn on the UI thread and waits for its result.
func (h *Host) Invoke(fn func() error) error {
	if fn == nil {
		return nil
	}
	done := make(chan error, 1)
	if !h.enqueue(hostTask{fn: fn, done: done}) {
		return errors.New("winui: host is not running")
	}
	return <-done
}

// Shutdown drains prior work, destroys the dispatcher window and waits for exit.
func (h *Host) Shutdown() {
	n := h.native()
	n.mu.Lock()
	started, stopped := n.started, n.stopped
	done := n.done
	n.mu.Unlock()
	if !started || stopped {
		return
	}
	_ = h.Invoke(func() error {
		n.mu.Lock()
		hwnd := n.hwnd
		n.mu.Unlock()
		if hwnd != 0 {
			procDestroyWindow.Call(hwnd)
		}
		return nil
	})
	<-done
}
