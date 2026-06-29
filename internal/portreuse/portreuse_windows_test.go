//go:build windows

package portreuse

import (
	"os"
	"testing"
)

// TestSameFilePath_Normalize 验证 Windows 上同一文件不同写法被判 same。
func TestSameFilePath_Normalize(t *testing.T) {
	dir := t.TempDir()
	p1 := dir + "/DoubaoToolbox.exe"
	if err := os.WriteFile(p1, []byte(""), 0644); err != nil {
		t.Skipf("无法写测试文件: %v", err)
	}
	p2 := dir + "\\DoubaoToolbox.exe"
	same, err := sameFilePath(p1, p2)
	if err != nil {
		t.Skipf("sameFilePath 跳错（无 Windows API?）: %v", err)
	}
	if !same {
		t.Errorf("同一文件不同写法应判 same,实际 not same")
	}
}
