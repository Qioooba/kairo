package httpserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKairoBrowserTitleSkipsNativeHost(t *testing.T) {
	if !isKairoBrowserTitle("Kairo · 天命契机") {
		t.Fatal("browser tab title should match")
	}
	if !isKairoBrowserTitle("文件与文本比较 - Kairo · 天命契机") {
		t.Fatal("document title with page prefix should match")
	}
	if isKairoBrowserTitle("KairoNativeHost") {
		t.Fatal("hidden native host window must not own the picker")
	}
	if isKairoBrowserTitle("Google Chrome") {
		t.Fatal("unrelated window should not match")
	}
}

func TestUsablePickerOwnerRejectsNativeHostAndTinyWindows(t *testing.T) {
	if usablePickerOwner("KairoNativeHost_v1", "KairoNativeHost", 1920*1080) {
		t.Fatal("native host must not own the picker")
	}
	if usablePickerOwner("Chrome_WidgetWin_1", "KairoNativeHost", 800*600) {
		t.Fatal("native host title must not own the picker")
	}
	if usablePickerOwner("Chrome_WidgetWin_1", "Kairo · 天命契机", 40*40) {
		t.Fatal("tiny window would pin the dialog at 0,0")
	}
	if !usablePickerOwner("Chrome_WidgetWin_1", "Kairo · 天命契机", 1280*800) {
		t.Fatal("browser window should own the picker")
	}
	if !usablePickerOwner("MozillaWindowClass", "文件与文本比较 - Kairo", 1100*700) {
		t.Fatal("titled browser window should own the picker")
	}
}

func TestPickerStartDirUsesInitialThenLast(t *testing.T) {
	t.Cleanup(func() { lastPicked = "" })
	dir := t.TempDir()
	nested := filepath.Join(dir, "only-this")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(nested, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pickerStartDir(nested); got != nested {
		t.Fatalf("directory initial: got %q want %q", got, nested)
	}
	if got := pickerStartDir(file); got != nested {
		t.Fatalf("file initial should open parent: got %q want %q", got, nested)
	}
	rememberPicked(nested)
	if got := pickerStartDir(""); got != nested {
		t.Fatalf("remembered path: got %q want %q", got, nested)
	}
}
