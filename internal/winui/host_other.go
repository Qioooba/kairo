//go:build !windows

package winui

// New creates a no-op host on non-Windows systems.
func New() *Host { return &Host{} }

// Start is a no-op on non-Windows systems.
func (h *Host) Start() error { return nil }

// Post executes fn immediately on non-Windows systems.
func (h *Host) Post(fn func()) bool {
	if fn != nil {
		fn()
	}
	return true
}

// Invoke executes fn immediately on non-Windows systems.
func (h *Host) Invoke(fn func() error) error {
	if fn == nil {
		return nil
	}
	return fn()
}

// Shutdown is a no-op on non-Windows systems.
func (h *Host) Shutdown() {}
