//go:build !windows

package schedtask

import "os"

func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}
