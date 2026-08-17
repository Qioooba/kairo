// Package sshclient — profilestore.go 实现"连接方案自适应记忆"。
//
// 背景：老 sshd（OpenSSH 6.2p2 / AIX / 老堡垒机）对 ECDH 等现代算法兼容性不一，
// Dial 内置 3 套 compat profile 按顺序自动 fallback。但每次连接都从第 1 套开始试，
// 对"需要 legacy 方案"的机器意味着每次都要先失败 1~2 次握手再成功，慢且会产生噪音。
//
// 本文件做的事：记住"每台 server 上次连接成功的 profile"，下次 Dial 时优先用它；
// 只有当它连不上时，才继续按原顺序尝试其它 profile，成功后再更新记忆。
// 全程对用户无感，记忆按 host:port 落盘到 data/ssh_compat_profiles.json。
//
// 安全：
//   - 只存 profile 名（不含密码 / host key / 用户信息），非敏感数据；
//   - 落盘用临时文件 + rename 原子替换，避免中途崩溃写坏文件；
//   - 文件权限 0600，与 credentials.json 一致。
package sshclient

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// profileStoreFileName 记忆文件的文件名，放在 data 目录下。
const profileStoreFileName = "ssh_compat_profiles.json"

var (
	storeMu       sync.Mutex
	storeDir      string
	storeLoaded   bool
	storeProfiles map[string]string // key = "host:port"，value = sshCompatProfile.Name
)

// SetProfileStoreDir 设置 profile 记忆的落盘目录（cfg.DataDir()）。
// 传入空字符串则禁用持久化（仅进程内记忆，重启丢失，测试/降级用）。
func SetProfileStoreDir(dir string) {
	storeMu.Lock()
	defer storeMu.Unlock()
	storeDir = dir
	storeLoaded = false // 换目录后下次访问重新从磁盘加载
}

func storeFilePath() string {
	return filepath.Join(storeDir, profileStoreFileName)
}

// loadProfilesLocked 懒加载磁盘上的记忆。必须在持有 storeMu 时调用。
func loadProfilesLocked() {
	if storeLoaded {
		return
	}
	storeLoaded = true
	storeProfiles = make(map[string]string)
	if storeDir == "" {
		return
	}
	data, err := os.ReadFile(storeFilePath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		return
	}
	_ = json.Unmarshal(data, &storeProfiles)
	if storeProfiles == nil {
		storeProfiles = make(map[string]string)
	}
}

// rememberProfile 记录 addr 这次连接成功的 profile 名，并落盘。
// 值没变化时跳过写盘，避免每次成功连接都写文件。
func rememberProfile(addr, profileName string) {
	if addr == "" || profileName == "" {
		return
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	loadProfilesLocked()
	if storeProfiles[addr] == profileName {
		return
	}
	storeProfiles[addr] = profileName
	if storeDir == "" {
		return
	}
	_ = os.MkdirAll(storeDir, 0o755)
	data, err := json.MarshalIndent(storeProfiles, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(storeDir, ".ssh_profiles-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmpName)
		return
	}
	_ = os.Chmod(tmpName, 0o600)
	if err := os.Rename(tmpName, storeFilePath()); err != nil {
		_ = os.Remove(tmpName)
	}
}

// rememberedProfile 返回 addr 上次连接成功的 profile 名（可能不存在）。
func rememberedProfile(addr string) (string, bool) {
	if addr == "" {
		return "", false
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	loadProfilesLocked()
	name, ok := storeProfiles[addr]
	if !ok || name == "" {
		return "", false
	}
	return name, true
}

// RememberedProfileFor 返回 host:port 这台机器上次连接成功的 profile 名。
// 没有记忆时返回空字符串（调用方据此展示"首次自动探测 / 未记忆"）。
// 供 diagnostics 等只读观测场景使用。
func RememberedProfileFor(host string, port int) string {
	if host == "" {
		return ""
	}
	if port == 0 {
		port = 22
	}
	name, _ := rememberedProfile(net.JoinHostPort(host, strconv.Itoa(port)))
	return name
}

// prioritizeProfiles 把"上次连接成功的 profile"挪到最前，其余保持原顺序。
//
//   - 列表长度 <= 1（用户显式 pin 了某个 profile，无 fallback 场景）不重排；
//   - 没有记忆、或记忆的 profile 已不在列表里（profile 列表变更）不重排；
//   - 已经排第一则不重排，直接返回原 slice。
func prioritizeProfiles(addr string, profiles []sshCompatProfile) []sshCompatProfile {
	if len(profiles) <= 1 {
		return profiles
	}
	name, ok := rememberedProfile(addr)
	if !ok {
		return profiles
	}
	idx := -1
	for i, p := range profiles {
		if p.Name == name {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return profiles
	}
	out := make([]sshCompatProfile, 0, len(profiles))
	out = append(out, profiles[idx])
	out = append(out, profiles[:idx]...)
	out = append(out, profiles[idx+1:]...)
	return out
}
