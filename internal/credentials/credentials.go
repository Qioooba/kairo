// Package credentials 把 SSH 密码存进 OS 钥匙串，跨平台：
//   - macOS   → Keychain（用 `security` CLI）
//   - Windows → DPAPI（wincred）
//   - Linux   → Secret Service / D-Bus
//
// 设计要点：
//   - Service 固定为 "OpsToolbox"，account 编码为 "system|server|username"；
//   - 永远不会把密码写进 audit.log、URL、错误信息或前端响应；
//   - keyring 不可用时返回 ErrUnavailable，前端提示用户"无法访问系统钥匙串"；
//   - v0.4 起支持配置化后端（项 23）：通过 SetMode 切换。
//     "keyring"（默认）走 OS 钥匙串；"file" 走文件密文（TODO，未实现）；
//     "disabled" 明确禁用——所有 Save/Get/Has/Clear 一律返回 ErrUnavailable。
package credentials

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// Service 是 keyring 的 service 字段，所有 OpsToolbox 存的密码都归在同一组。
const Service = "OpsToolbox"

// Mode 凭据后端模式常量（项 23）
const (
	ModeKeyring  = "keyring"  // 默认：OS 钥匙串
	ModeFile     = "file"     // 本地加密文件（v0.4 起配置化占位）
	ModeDisabled = "disabled" // 明确禁用：不持久化密码
)

// ErrUnavailable keyring 在当前平台不可用（比如 Linux 没装 gnome-keyring / KWallet），
// 或当前 mode 不允许此操作（mode=disabled 时所有接口都返 ErrUnavailable）
var ErrUnavailable = errors.New("credentials: 系统钥匙串不可用")

// ErrNotSaved 指定 (system, server, username) 三元组下没有保存过密码
var ErrNotSaved = errors.New("credentials: 未保存密码")

// ErrFileNotImplemented mode=file 暂未实现加密文件存储，调用方应回退到 keyring 或 disabled
var ErrFileNotImplemented = errors.New("credentials: file 模式暂未实现（v0.4 起配置化占位）")

// modeMu 保护 mode 的读写并发安全。SetMode 在 main 启动时调一次，
// 之后 httpserver 等 handler 读 Mode() 时加 RLock。
var (
	modeMu sync.RWMutex
	mode   = ModeKeyring
)

// SetMode 切换凭据后端模式。
//
//   - 接受 "keyring" / "file" / "disabled"（大小写、首尾空格都容忍）；
//   - 未知值兜底为 "keyring"，保证向后兼容；
//   - 建议在 main 启动时、配置加载后立即调一次。
//
// 设计上不引入 config 包依赖，避免循环依赖。
func SetMode(m string) {
	modeMu.Lock()
	defer modeMu.Unlock()
	switch strings.ToLower(strings.TrimSpace(m)) {
	case ModeFile:
		mode = ModeFile
	case ModeDisabled, "off", "none":
		mode = ModeDisabled
	default:
		mode = ModeKeyring
	}
}

// Mode 返回当前凭据后端模式（"keyring"/"file"/"disabled"）。
// handler 可以把这个值塞进 /api/credentials/* 响应，让前端决定是否渲染「记住密码」控件。
func Mode() string {
	modeMu.RLock()
	defer modeMu.RUnlock()
	return mode
}

// guardMode 在 disabled 模式下拦截所有凭据操作。
// file 模式暂未实现，同样拒绝并提示 ErrFileNotImplemented，避免静默写入失败。
func guardMode() error {
	switch Mode() {
	case ModeDisabled:
		return fmt.Errorf("%w（配置 credential_store=disabled）", ErrUnavailable)
	case ModeFile:
		return ErrFileNotImplemented
	default:
		return nil
	}
}

// Key 由 (system, server, username) 拼出的 keyring account 字符串。
// 用 | 分隔，| 在 system/server/username 中不会出现（system/server 是配置项，
// 配置校验已经禁了 |；username 是 SSH 用户名，不会含 |）。
func Key(system, server, username string) string {
	return strings.Join([]string{system, server, username}, "|")
}

// Save 把密码存到 keyring。同一个 (system, server, username) 重复保存会覆盖。
func Save(system, server, username, password string) error {
	if system == "" || server == "" || username == "" {
		return errors.New("credentials: system/server/username 不能为空")
	}
	if password == "" {
		return errors.New("credentials: 密码不能为空")
	}
	if err := guardMode(); err != nil {
		return err
	}
	if err := keyring.Set(Service, Key(system, server, username), password); err != nil {
		// 区分"keyring 不可用"和"其它错误"（前者是预期会发生的，后者是 bug）
		if isUnavailable(err) {
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return fmt.Errorf("credentials: 写入钥匙串失败: %w", err)
	}
	return nil
}

// Get 取出指定 (system, server, username) 对应的密码。
// 没有时返回 ErrNotSaved；keyring 不可用时返回 ErrUnavailable。
func Get(system, server, username string) (string, error) {
	if system == "" || server == "" || username == "" {
		return "", errors.New("credentials: system/server/username 不能为空")
	}
	if err := guardMode(); err != nil {
		return "", err
	}
	pw, err := keyring.Get(Service, Key(system, server, username))
	if err != nil {
		// zalando 在没找到时返回 ErrNotFound；其它错误可能是 keychain locked 等
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNotSaved
		}
		if isUnavailable(err) {
			return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return "", fmt.Errorf("credentials: 读取钥匙串失败: %w", err)
	}
	return pw, nil
}

// Has 检查指定 (system, server, username) 是否已保存密码（不返回密码本身）。
func Has(system, server, username string) (bool, error) {
	_, err := Get(system, server, username)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotSaved) {
		return false, nil
	}
	return false, err
}

// Clear 删除指定 (system, server, username) 的密码。
// 没有时返回 ErrNotSaved（前端可以用来提示"本来就没保存"）。
func Clear(system, server, username string) error {
	if system == "" || server == "" || username == "" {
		return errors.New("credentials: system/server/username 不能为空")
	}
	if err := guardMode(); err != nil {
		return err
	}
	if err := keyring.Delete(Service, Key(system, server, username)); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return ErrNotSaved
		}
		if isUnavailable(err) {
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return fmt.Errorf("credentials: 删除钥匙串条目失败: %w", err)
	}
	return nil
}

// isUnavailable 识别"keyring 不可用"的错误（DBus 没起 / Keychain 拒绝访问 / wincred 失败）。
// 不同后端的错误信息不统一，所以用关键字兜底判断。
func isUnavailable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, hint := range []string{
		"dbus", "secret service", "no such interface",
		"could not connect", "the user name or password is incorrect", // Windows CredUI 拒绝
		"keychain", "errse", "access denied",
		"org.freedesktop.secrets", "collection",
	} {
		if strings.Contains(s, hint) {
			return true
		}
	}
	return false
}
