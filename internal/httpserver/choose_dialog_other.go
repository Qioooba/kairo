//go:build !darwin && !windows

package httpserver

import "errors"

func chooseFile() (string, error) { return chooseFileAt("") }
func chooseDir() (string, error)  { return chooseDirAt("") }

func chooseFileAt(initial string) (string, error) {
	return "", errors.New("当前平台暂不支持文件选择对话框")
}
func chooseDirAt(initial string) (string, error) {
	return "", errors.New("当前平台暂不支持文件夹选择对话框")
}
