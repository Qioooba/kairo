package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const homeEnv = "KAIRO_HOME"

// Location 描述配置文件及其相对路径基准目录。
type Location struct {
	RootDir    string
	ConfigPath string
}

// ResolveLocation 确定稳定的用户配置目录。优先级：--config、KAIRO_HOME、--portable、系统用户配置目录。
func ResolveLocation(explicitPath string, portable bool) (Location, error) {
	if value := strings.TrimSpace(explicitPath); value != "" {
		path, err := filepath.Abs(value)
		if err != nil {
			return Location{}, fmt.Errorf("解析 --config 路径失败: %w", err)
		}
		return Location{RootDir: filepath.Dir(path), ConfigPath: path}, nil
	}

	if value := strings.TrimSpace(os.Getenv(homeEnv)); value != "" {
		root, err := filepath.Abs(value)
		if err != nil {
			return Location{}, fmt.Errorf("解析 %s 失败: %w", homeEnv, err)
		}
		return locationFromRoot(root), nil
	}

	if portable {
		exe, err := os.Executable()
		if err != nil {
			return Location{}, fmt.Errorf("定位程序目录失败: %w", err)
		}
		root := filepath.Dir(exe)
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			root = filepath.Dir(resolved)
		}
		return locationFromRoot(root), nil
	}

	root, err := os.UserConfigDir()
	if err != nil {
		return Location{}, fmt.Errorf("定位用户配置目录失败: %w", err)
	}
	return locationFromRoot(filepath.Join(root, "Kairo")), nil
}

func locationFromRoot(root string) Location {
	root = filepath.Clean(root)
	return Location{RootDir: root, ConfigPath: filepath.Join(root, "config.yaml")}
}
