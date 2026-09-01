package httpserver

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	pickerMu     sync.Mutex
	lastPickedMu sync.RWMutex
	lastPicked   string
	uiInvoke     func(func() error) error
)

// SetUIInvoker runs native file/folder pickers on the Windows UI thread so
// IFileOpenDialog is owned by a window with a message loop instead of an HTTP
// goroutine (which previously placed the dialog at screen 0,0).
func SetUIInvoker(fn func(func() error) error) {
	uiInvoke = fn
}

func invokePicker(fn func() (string, error)) (string, error) {
	if uiInvoke == nil {
		return fn()
	}
	var path string
	err := uiInvoke(func() error {
		var inner error
		path, inner = fn()
		return inner
	})
	return path, err
}

func isKairoBrowserTitle(title string) bool {
	lower := strings.ToLower(strings.TrimSpace(title))
	if lower == "" || strings.Contains(lower, "kaironativehost") {
		return false
	}
	return strings.Contains(lower, "kairo")
}

const pickerMinOwnerArea = 200 * 200

// usablePickerOwner rejects the hidden 0×0 native host and tiny windows.
// Passing those as IFileOpenDialog owners places the picker at screen (0,0).
func usablePickerOwner(class, title string, area int) bool {
	if strings.EqualFold(strings.TrimSpace(class), "KairoNativeHost_v1") {
		return false
	}
	if strings.Contains(strings.ToLower(title), "kaironativehost") {
		return false
	}
	return area >= pickerMinOwnerArea
}

func rememberPicked(path string) {
	path = filepath.Clean(path)
	if path != "" && path != "." {
		lastPickedMu.Lock()
		lastPicked = path
		lastPickedMu.Unlock()
	}
}

func getLastPicked() string {
	lastPickedMu.RLock()
	defer lastPickedMu.RUnlock()
	return lastPicked
}

func pickerStartDir(initial string) string {
	candidates := []string{initial, getLastPicked()}
	if home, err := os.UserHomeDir(); err == nil {
		desktop := filepath.Join(home, "Desktop")
		candidates = append(candidates, desktop, home)
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if candidate == "" || candidate == "." {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return candidate
		}
		return filepath.Dir(candidate)
	}
	return ""
}
