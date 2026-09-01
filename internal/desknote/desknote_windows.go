//go:build windows

// Package desknote presents Kairo notes as native Windows 10 desktop windows.
package desknote

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"kairo/internal/note"
	"kairo/internal/winui"
)

const (
	noteClass = "KairoDesktopNote_v1"

	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsThickFrame   = 0x00040000
	wsMinimizeBox  = 0x00020000
	wsTabStop      = 0x00010000
	wsVScroll      = 0x00200000
	wsExClientEdge = 0x00000200
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080
	esMultiline    = 0x0004
	esAutoVScroll  = 0x0040
	esWantReturn   = 0x1000
	esAutoHScroll  = 0x0080

	wmDestroy      = 0x0002
	wmSize         = 0x0005
	wmMove         = 0x0003
	wmClose        = 0x0010
	wmCommand      = 0x0111
	wmCtlColorEdit = 0x0133
	wmSetFont      = 0x0030
	wmGetText      = 0x000D
	wmGetTextLen   = 0x000E
	enChange       = 0x0300
	swShow         = 5
	spiGetWorkArea = 0x0030
	swpNoActivate  = 0x0010
	swpNoZOrder    = 0x0004
	defaultGUIFont = 17

	wmUser          = 0x0400
	emSetBkgndColor = wmUser + 67
	emSetCharFormat = wmUser + 68
	emSetEventMask  = wmUser + 69
	emSetTextMode   = wmUser + 88
	tmPlaintext     = 1
	enmChange       = 1
	scfAll          = 4
	cfmColor        = 0x40000000
	mbOk            = 0x00000000
	mbYesNoCancel   = 0x00000003
	mbIconWarning   = 0x00000030
	mbTopmost       = 0x00040000
	idYes           = 6
	idNo            = 7
)

type Controller struct {
	host    *winui.Host
	manager *note.Manager

	mu      sync.Mutex
	windows map[string]*desktopWindow
	closing bool
	stop    chan struct{}
	unsub   func()
	once    sync.Once
}

type desktopWindow struct {
	controller *Controller
	note       note.Note
	hwnd       uintptr
	title      uintptr
	body       uintptr
	brush      uintptr
	suppress   bool
	dirty      bool
	conflict   bool
	sequence   uint64
}

type rect struct{ Left, Top, Right, Bottom int32 }
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

type charFormatW struct {
	cbSize          uint32
	dwMask          uint32
	dwEffects       uint32
	yHeight         int32
	yOffset         int32
	crTextColor     uint32
	bCharSet        byte
	bPitchAndFamily byte
	szFaceName      [32]uint16
}

var (
	user32                    = windows.NewLazySystemDLL("user32.dll")
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	gdi32                     = windows.NewLazySystemDLL("gdi32.dll")
	procRegisterClassExW      = user32.NewProc("RegisterClassExW")
	procCreateWindowExW       = user32.NewProc("CreateWindowExW")
	procDefWindowProcW        = user32.NewProc("DefWindowProcW")
	procDestroyWindow         = user32.NewProc("DestroyWindow")
	procShowWindow            = user32.NewProc("ShowWindow")
	procMoveWindow            = user32.NewProc("MoveWindow")
	procSetWindowPos          = user32.NewProc("SetWindowPos")
	procGetWindowRect         = user32.NewProc("GetWindowRect")
	procSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")
	procSendMessageW          = user32.NewProc("SendMessageW")
	procSetWindowTextW        = user32.NewProc("SetWindowTextW")
	procMessageBoxW           = user32.NewProc("MessageBoxW")
	procGetModuleHandleW      = kernel32.NewProc("GetModuleHandleW")
	procLoadLibraryW          = kernel32.NewProc("LoadLibraryW")
	procGetStockObject        = gdi32.NewProc("GetStockObject")
	procSetBkColor            = gdi32.NewProc("SetBkColor")
	procSetTextColor          = gdi32.NewProc("SetTextColor")
	procCreateSolidBrush      = gdi32.NewProc("CreateSolidBrush")
	procDeleteObject          = gdi32.NewProc("DeleteObject")

	windowsMu           sync.RWMutex
	windowsByHWND       = map[uintptr]*desktopWindow{}
	noteWndProcCallback = syscall.NewCallback(noteWndProc)
)

func New(host *winui.Host, manager *note.Manager) (*Controller, error) {
	if host == nil || manager == nil {
		return nil, errors.New("desknote: host 和 manager 不能为空")
	}
	c := &Controller{host: host, manager: manager, windows: map[string]*desktopWindow{}, stop: make(chan struct{})}
	if err := host.Invoke(func() error {
		if err := registerNoteClass(); err != nil {
			return err
		}
		dll, _ := windows.UTF16PtrFromString("Msftedit.dll")
		procLoadLibraryW.Call(uintptr(unsafe.Pointer(dll)))
		for _, n := range manager.List(note.Filter{DesktopOnly: true}) {
			if err := c.applyNote(n); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	events, unsub := manager.Subscribe()
	c.unsub = unsub
	go func() {
		for {
			select {
			case ev := <-events:
				c.mu.Lock()
				closing := c.closing
				c.mu.Unlock()
				if closing {
					continue
				}
				if ev.Kind == "deleted" {
					c.host.Post(func() { c.removeWindow(ev.ID) })
				} else if ev.Note != nil {
					n := *ev.Note
					c.host.Post(func() { _ = c.applyNote(n) })
				}
			case <-c.stop:
				return
			}
		}
	}()
	return c, nil
}

func registerNoteClass() error {
	name, _ := windows.UTF16PtrFromString(noteClass)
	instance, _, _ := procGetModuleHandleW.Call(0)
	wc := wndClassEx{CbSize: uint32(unsafe.Sizeof(wndClassEx{})), LpfnWndProc: noteWndProcCallback, HInstance: instance, LpszClassName: name}
	r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if r == 0 && err != syscall.Errno(1410) {
		return fmt.Errorf("desknote: 注册窗口类: %w", err)
	}
	return nil
}

func (c *Controller) applyNote(n note.Note) error {
	c.mu.Lock()
	w := c.windows[n.ID]
	c.mu.Unlock()
	if n.Archived || n.Desktop == nil || !n.Desktop.Visible {
		if w != nil {
			c.removeWindow(n.ID)
		}
		return nil
	}
	if w == nil {
		return c.createWindow(n)
	}
	if w.conflict {
		return nil
	}
	if w.dirty {
		w.note.Revision = n.Revision
		return nil
	}
	w.suppress = true
	defer func() { w.suppress = false }()
	applyPaper(w, n.Color)
	w.note = n
	setTextIfChanged(w.title, n.Title)
	setTextIfChanged(w.body, n.Body)
	setWindowText(w.hwnd, n.DisplayTitle()+" · Kairo 便笺")
	setWindowLayout(w.hwnd, *n.Desktop)
	procShowWindow.Call(w.hwnd, swShow)
	return nil
}

func (c *Controller) createWindow(n note.Note) error {
	class, _ := windows.UTF16PtrFromString(noteClass)
	title, _ := windows.UTF16PtrFromString(n.DisplayTitle() + " · Kairo 便笺")
	instance, _, _ := procGetModuleHandleW.Call(0)
	l := *n.Desktop
	x, y, width, height := layoutPixels(l)
	hwnd, _, err := procCreateWindowExW.Call(wsExTopmost|wsExToolWindow, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), wsCaption|wsSysMenu|wsThickFrame|wsMinimizeBox, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("desknote: 创建窗口失败: %w", err)
	}
	brush, _, _ := procCreateSolidBrush.Call(uintptr(noteColor(n.Color)))
	w := &desktopWindow{controller: c, note: n, hwnd: hwnd, brush: brush, suppress: true}
	windowsMu.Lock()
	windowsByHWND[hwnd] = w
	windowsMu.Unlock()
	c.mu.Lock()
	c.windows[n.ID] = w
	c.mu.Unlock()

	w.title = createChild(hwnd, "EDIT", n.Title, wsTabStop|esAutoHScroll, 10, 10, width-36, 28)
	w.body = createBody(hwnd, n.Body, n.Color, 10, 46, width-36, height-96)
	font, _, _ := procGetStockObject.Call(defaultGUIFont)
	procSendMessageW.Call(w.title, wmSetFont, font, 1)
	procSendMessageW.Call(w.body, wmSetFont, font, 1)
	w.suppress = false
	procShowWindow.Call(hwnd, swShow)
	return nil
}

func createChild(parent uintptr, className, value string, style uint32, x, y, width, height int32) uintptr {
	class, _ := windows.UTF16PtrFromString(className)
	text, _ := windows.UTF16PtrFromString(value)
	instance, _, _ := procGetModuleHandleW.Call(0)
	hwnd, _, _ := procCreateWindowExW.Call(wsExClientEdge, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(text)), uintptr(wsChild|wsVisible|style), uintptr(x), uintptr(y), uintptr(width), uintptr(height), parent, 0, instance, 0)
	return hwnd
}

func createBody(parent uintptr, value, color string, x, y, width, height int32) uintptr {
	style := uint32(wsTabStop | wsVScroll | esMultiline | esAutoVScroll | esWantReturn)
	hwnd := createChild(parent, "RICHEDIT50W", "", style, x, y, width, height)
	if hwnd == 0 {
		return createChild(parent, "EDIT", value, style, x, y, width, height)
	}
	procSendMessageW.Call(hwnd, emSetTextMode, tmPlaintext, 0)
	procSendMessageW.Call(hwnd, emSetEventMask, 0, enmChange)
	applyRichPaper(hwnd, color)
	setWindowText(hwnd, value)
	return hwnd
}

func applyPaper(w *desktopWindow, color string) {
	if w.note.Color == color && w.brush != 0 {
		applyRichPaper(w.body, color)
		return
	}
	if w.brush != 0 {
		procDeleteObject.Call(w.brush)
	}
	w.brush, _, _ = procCreateSolidBrush.Call(uintptr(noteColor(color)))
	applyRichPaper(w.body, color)
}

func applyRichPaper(hwnd uintptr, color string) {
	if hwnd == 0 {
		return
	}
	procSendMessageW.Call(hwnd, emSetBkgndColor, 0, uintptr(noteColor(color)))
	cf := charFormatW{cbSize: uint32(unsafe.Sizeof(charFormatW{})), dwMask: cfmColor, crTextColor: rgb(45, 39, 29)}
	procSendMessageW.Call(hwnd, emSetCharFormat, scfAll, uintptr(unsafe.Pointer(&cf)))
}

func noteWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	windowsMu.RLock()
	w := windowsByHWND[hwnd]
	windowsMu.RUnlock()
	if w != nil {
		switch uint32(msg) {
		case wmSize:
			width, height := int32(uint16(lParam)), int32(uint16(lParam>>16))
			if w.title != 0 {
				procMoveWindow.Call(w.title, 10, 10, uintptr(max32(80, width-20)), 28, 1)
			}
			if w.body != 0 {
				procMoveWindow.Call(w.body, 10, 46, uintptr(max32(80, width-20)), uintptr(max32(50, height-56)), 1)
			}
			w.scheduleSave()
			return 0
		case wmMove:
			w.scheduleSave()
		case wmCommand:
			if uint16(wParam>>16) == enChange && (lParam == w.title || lParam == w.body) {
				w.dirty = true
				w.scheduleSave()
			}
		case wmClose:
			w.hide()
			return 0
		case wmCtlColorEdit:
			procSetBkColor.Call(wParam, uintptr(noteColor(w.note.Color)))
			procSetTextColor.Call(wParam, uintptr(rgb(45, 39, 29)))
			return w.brush
		case wmDestroy:
			windowsMu.Lock()
			delete(windowsByHWND, hwnd)
			windowsMu.Unlock()
			return 0
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

func (w *desktopWindow) scheduleSave() {
	if w.suppress || w.hwnd == 0 || w.conflict {
		return
	}
	w.sequence++
	seq := w.sequence
	time.AfterFunc(550*time.Millisecond, func() { w.save(seq) })
}

func (w *desktopWindow) save(seq uint64) {
	type snapshot struct {
		id        string
		title     string
		body      string
		layout    note.DesktopLayout
		revision  uint64
		textDirty bool
		valid     bool
	}
	var s snapshot
	if err := w.controller.host.Invoke(func() error {
		if w.hwnd == 0 || w.sequence != seq || w.conflict {
			return nil
		}
		s = snapshot{
			id: w.note.ID, title: getText(w.title), body: getText(w.body),
			layout: windowLayout(w.hwnd), revision: w.note.Revision,
			textDirty: w.dirty, valid: true,
		}
		return nil
	}); err != nil || !s.valid {
		return
	}
	desktop := &s.layout
	patch := note.Patch{BaseRevision: s.revision, Desktop: &desktop}
	if s.textDirty {
		patch.Title = &s.title
		patch.Body = &s.body
	}
	updated, err := w.controller.manager.Patch(s.id, patch)
	if conflict := new(note.ConflictError); errors.As(err, &conflict) {
		if !s.textDirty {
			patch.BaseRevision = conflict.Current.Revision
			updated, err = w.controller.manager.Patch(s.id, patch)
		} else {
			w.controller.host.Post(func() { w.resolveConflict(conflict.Current, s.title, s.body, s.layout) })
			return
		}
	}
	if err == nil {
		w.controller.host.Post(func() { w.note = updated; w.dirty = false })
	}
}

func (w *desktopWindow) resolveConflict(server note.Note, localTitle, localBody string, layout note.DesktopLayout) {
	if w.hwnd == 0 {
		return
	}
	w.conflict = true
	choice := messageBox(w.hwnd, "这条便笺已在其他窗口更新。\n\n是：保留桌面上正在编辑的内容\n否：改用其他窗口的版本\n取消：先不保存", "便笺版本冲突", mbYesNoCancel|mbIconWarning|mbTopmost)
	switch choice {
	case idYes:
		desktop := &layout
		updated, err := w.controller.manager.Patch(w.note.ID, note.Patch{
			BaseRevision: server.Revision, Title: &localTitle, Body: &localBody, Desktop: &desktop,
		})
		if err == nil {
			w.note = updated
			w.dirty = false
			w.conflict = false
			return
		}
		messageBox(w.hwnd, err.Error(), "Kairo 便笺", mbOk|mbIconWarning|mbTopmost)
		w.conflict = false
	case idNo:
		w.suppress = true
		w.note = server
		w.dirty = false
		setTextIfChanged(w.title, server.Title)
		setTextIfChanged(w.body, server.Body)
		applyPaper(w, server.Color)
		setWindowText(w.hwnd, server.DisplayTitle()+" · Kairo 便笺")
		w.suppress = false
		w.conflict = false
	default:
		w.conflict = false
	}
}

func (w *desktopWindow) hide() {
	if w.hwnd == 0 {
		return
	}
	layout := windowLayout(w.hwnd)
	layout.Visible = false
	desktop := &layout
	title, body := getText(w.title), getText(w.body)
	procShowWindow.Call(w.hwnd, 0)
	patch := note.Patch{BaseRevision: w.note.Revision, Desktop: &desktop}
	if w.dirty {
		patch.Title = &title
		patch.Body = &body
	}
	_, err := w.controller.manager.Patch(w.note.ID, patch)
	if conflict := new(note.ConflictError); errors.As(err, &conflict) {
		patch.BaseRevision = conflict.Current.Revision
		if w.dirty {
			patch.Title = &title
			patch.Body = &body
		}
		_, _ = w.controller.manager.Patch(w.note.ID, patch)
	}
}

func (c *Controller) removeWindow(id string) {
	c.mu.Lock()
	w := c.windows[id]
	delete(c.windows, id)
	c.mu.Unlock()
	if w == nil {
		return
	}
	w.sequence++
	hwnd := w.hwnd
	brush := w.brush
	w.hwnd, w.title, w.body = 0, 0, 0
	w.brush = 0
	if hwnd != 0 {
		procDestroyWindow.Call(hwnd)
	}
	if brush != 0 {
		procDeleteObject.Call(brush)
	}
}

func (c *Controller) NewNote() error {
	_, err := c.manager.Add(note.Note{Color: "yellow", Desktop: note.DefaultDesktop()})
	if err != nil {
		c.host.Post(func() { messageBox(0, err.Error(), "Kairo 便笺", mbOk|mbIconWarning|mbTopmost) })
	}
	return err
}

func (c *Controller) ToggleAll() error {
	items := c.manager.List(note.Filter{})
	visible := false
	for _, n := range items {
		if n.DesktopVisible() {
			visible = true
			break
		}
	}
	if visible {
		for _, n := range items {
			if n.Archived || n.Desktop == nil || !n.Desktop.Visible {
				continue
			}
			layout := *n.Desktop
			layout.Visible = false
			desktop := &layout
			if _, err := c.manager.Patch(n.ID, note.Patch{BaseRevision: n.Revision, Desktop: &desktop}); err != nil {
				return err
			}
		}
		return nil
	}
	shown := 0
	for _, n := range items {
		if n.Archived || n.Desktop == nil {
			continue
		}
		if shown >= note.MaxDesktopVisible {
			break
		}
		layout := *n.Desktop
		layout.Visible = true
		desktop := &layout
		if _, err := c.manager.Patch(n.ID, note.Patch{BaseRevision: n.Revision, Desktop: &desktop}); err != nil {
			return err
		}
		shown++
	}
	if shown == 0 {
		return c.NewNote()
	}
	return nil
}

func (c *Controller) Shutdown() {
	c.once.Do(func() {
		c.mu.Lock()
		c.closing = true
		c.mu.Unlock()
		close(c.stop)
		if c.unsub != nil {
			c.unsub()
		}
		_ = c.host.Invoke(func() error {
			c.mu.Lock()
			ids := make([]string, 0, len(c.windows))
			for id := range c.windows {
				ids = append(ids, id)
			}
			c.mu.Unlock()
			for _, id := range ids {
				c.mu.Lock()
				w := c.windows[id]
				c.mu.Unlock()
				if w != nil && w.hwnd != 0 {
					title, body := getText(w.title), getText(w.body)
					layout := windowLayout(w.hwnd)
					desktop := &layout
					patch := note.Patch{BaseRevision: w.note.Revision, Desktop: &desktop}
					if w.dirty {
						patch.Title = &title
						patch.Body = &body
					}
					if _, err := w.controller.manager.Patch(id, patch); err != nil {
						if conflict := new(note.ConflictError); errors.As(err, &conflict) {
							patch.BaseRevision = conflict.Current.Revision
							_, _ = w.controller.manager.Patch(id, patch)
						}
					}
				}
				c.removeWindow(id)
			}
			return nil
		})
	})
}

func layoutPixels(l note.DesktopLayout) (int32, int32, int32, int32) {
	wa := workArea()
	w, h := int32(l.Width), int32(l.Height)
	if w < 240 {
		w = 340
	}
	if h < 120 {
		h = 280
	}
	dx, dy := max32(0, wa.Right-wa.Left-w), max32(0, wa.Bottom-wa.Top-h)
	return wa.Left + int32(float64(dx)*l.XRatio), wa.Top + int32(float64(dy)*l.YRatio), w, h
}

func setWindowLayout(hwnd uintptr, l note.DesktopLayout) {
	x, y, w, h := layoutPixels(l)
	cur := getWindowRect(hwnd)
	if cur.Left != x || cur.Top != y || cur.Right-cur.Left != w || cur.Bottom-cur.Top != h {
		procSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoActivate|swpNoZOrder)
	}
}

func windowLayout(hwnd uintptr) note.DesktopLayout {
	wa, r := workArea(), getWindowRect(hwnd)
	w, h := r.Right-r.Left, r.Bottom-r.Top
	dx, dy := max32(1, wa.Right-wa.Left-w), max32(1, wa.Bottom-wa.Top-h)
	return note.DesktopLayout{Visible: true, XRatio: clamp(float64(r.Left-wa.Left) / float64(dx)), YRatio: clamp(float64(r.Top-wa.Top) / float64(dy)), Width: int(w), Height: int(h)}
}

func workArea() rect {
	var r rect
	procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0)
	return r
}
func getWindowRect(hwnd uintptr) rect {
	var r rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}
func setWindowText(hwnd uintptr, s string) {
	p, _ := windows.UTF16PtrFromString(s)
	procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(p)))
}
func setTextIfChanged(hwnd uintptr, s string) {
	if hwnd != 0 && getText(hwnd) != s {
		setWindowText(hwnd, s)
	}
}
func getText(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	n, _, _ := procSendMessageW.Call(hwnd, wmGetTextLen, 0, 0)
	buf := make([]uint16, n+1)
	procSendMessageW.Call(hwnd, wmGetText, n+1, uintptr(unsafe.Pointer(&buf[0])))
	return windows.UTF16ToString(buf)
}
func messageBox(hwnd uintptr, text, caption string, flags uint32) int {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(caption)
	r, _, _ := procMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), uintptr(flags))
	return int(r)
}
func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func rgb(r, g, b uint32) uint32 { return r | g<<8 | b<<16 }

func noteColor(color string) uint32 {
	switch color {
	case "blue":
		return rgb(217, 239, 255)
	case "green":
		return rgb(220, 245, 223)
	case "pink":
		return rgb(255, 224, 235)
	case "gray":
		return rgb(232, 235, 239)
	default:
		return rgb(255, 242, 184)
	}
}
