//go:build windows

package popup

import (
	"runtime"
	"testing"
)

func TestPopupCursorResetsAfterBusyAndClose(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	getCursor := user32DLL().NewProc("GetCursor")
	previous, _, _ := getCursor.Call()
	defer procSetCursor.Call(previous)
	busy, _, _ := procLoadCursorW.Call(0, 32514) // IDC_WAIT
	procSetCursor.Call(busy)
	for _, tc := range []struct {
		name string
		x, y uintptr
		want uintptr
	}{
		{"busy to body", 24, 60, IDC_ARROW},
		{"body to close", popupW - 24, 24, IDC_HAND},
		{"close to body", 24, 60, IDC_ARROW},
	} {
		wndProc(0, WM_MOUSEMOVE, 0, tc.x|tc.y<<16)
		got, _, _ := getCursor.Call()
		want, _, _ := procLoadCursorW.Call(0, tc.want)
		if want == 0 || got != want {
			t.Fatalf("%s: cursor = %v, want %v", tc.name, got, want)
		}
	}
}

func TestPopupColorRef(t *testing.T) {
	if got := colorRef(0x123456); got != 0x563412 {
		t.Fatalf("COLORREF = %#x, want 0x563412", got)
	}
}
