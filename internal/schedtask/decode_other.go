//go:build !windows

package schedtask

import "errors"

func decodePlatformOutput(raw []byte) (string, error) {
	return string(raw), errors.New("当前平台无额外系统编码解码器")
}
