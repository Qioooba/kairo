// Package winui owns the Windows desktop UI thread used by Kairo native windows.
package winui

// Host serializes native window work onto one Windows message-loop thread.
// The non-Windows implementation is a harmless in-process executor so callers
// can keep one lifecycle on every platform.
type Host struct{ nativeState any }
