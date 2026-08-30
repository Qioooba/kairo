//go:build windows

package schedtask

import (
	"os"
	"time"
)

// replaceFile 吸收 Windows Defender、杀毒和备份软件造成的短暂
// ACCESS_DENIED / SHARING_VIOLATION。临时文件与目标文件位于同一目录，
// 因此每次尝试仍是同卷原子替换，不采用“先删目标再改名”的数据窗口。
func replaceFile(src, dst string) error {
	const attempts = 5
	delay := 40 * time.Millisecond
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := os.Rename(src, dst); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i+1 < attempts {
			time.Sleep(delay)
			delay *= 2
		}
	}
	return lastErr
}
