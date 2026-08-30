//go:build windows

package schedtask

import (
	"errors"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

var getOEMCP = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetOEMCP")

// decodePlatformOutput 按当前 Windows OEM code page 解码 cmd.exe 的重定向输出。
// 这样同时覆盖 CP936/950/932/437 等系统，而不是把所有非 UTF-8 输出都误当 GBK。
func decodePlatformOutput(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	cp, _, callErr := getOEMCP.Call()
	if cp == 0 {
		return "", callErr
	}
	n, err := windows.MultiByteToWideChar(uint32(cp), 0, &raw[0], int32(len(raw)), nil, 0)
	if err != nil || n <= 0 {
		if err == nil {
			err = errors.New("MultiByteToWideChar 返回空结果")
		}
		return "", err
	}
	wide := make([]uint16, n)
	n, err = windows.MultiByteToWideChar(uint32(cp), 0, &raw[0], int32(len(raw)), &wide[0], int32(len(wide)))
	if err != nil {
		return "", err
	}
	return string(utf16.Decode(wide[:n])), nil
}
