//go:build !darwin && !windows

package httpserver

import "errors"

func chooseFile() (string, error) {
	return "", errors.New("当前平台暂不支持文件选择对话框")
}
func chooseDir() (string, error) {
	return "", errors.New("当前平台暂不支持文件夹选择对话框")
}
