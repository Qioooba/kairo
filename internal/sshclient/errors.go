// Package sshclient: SSH 错误分类（P3-2）
//
// 目标：把 x/crypto/ssh 返回的英文错误信息归类到运维友好的中文类别。
//
// 背景：
//   - x/crypto/ssh 在不同失败场景下返回的错误信息五花八门，
//     例如握手 RST 时报 "ssh: handshake failed: ssh: no matching algorithms",
//     认证失败时报 "ssh: handshake failed: ssh: unable to authenticate, ..."
//   - 直接把英文错误扔给前端既不友好，也不利于统计/告警；
//   - 旧实现只调 sshclient.SanitizeError 把密码字面量遮掉，类别没结构化。
//
// 设计：
//   - Categorize(err) 返回一个 Category 常量；
//   - 同时导出 Diagnose(err) 返回 {Category, Reason, Suggestion, Raw}，
//     Reason 是面向用户的中文短句，Suggestion 是建议下一步动作（前端可折叠展示）；
//   - 不修改 sshclient.go 的行为，只新增文件；老 handler 拿 err 后调
//     sshclient.Categorize/ sshclient.Diagnose 即可升级。
package sshclient

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

// Category SSH 错误类别（前端可用作统计 / 筛选 / 配色）
type Category string

const (
	CatUnknown   Category = "unknown"
	CatDNS       Category = "dns"                  // DNS 解析失败（no such host）
	CatNetwork   Category = "network"              // TCP 连接失败（i/o timeout, no route, refused）
	CatPort      Category = "port"                 // 端口不可达（connection refused）
	CatTimeout   Category = "timeout"              // ctx / 内部 timer 超时
	CatHandshake Category = "handshake"            // 协议握手失败（算法 / KEX 不匹配）
	CatHostKey   Category = "host_key"             // host key 不匹配 / 不受信任
	CatAuth      Category = "auth"                 // 密码错 / 不支持认证方法 / 账号锁
	CatKbdInt    Category = "keyboard-interactive" // PAM / keyboard-interactive 失败
	CatSFTP      Category = "sftp"                 // SFTP 子系统失败
	CatCommand   Category = "command"              // 远端命令非 0 / context 取消
)

// Diagnosis 错误诊断结果
type Diagnosis struct {
	Category   Category `json:"category"`
	Reason     string   `json:"reason"`     // 中文短句（前端直接展示）
	Suggestion string   `json:"suggestion"` // 中文建议（可折叠）
	Raw        string   `json:"raw"`        // 原始 sanitized 错误（已脱敏）
}

// Diagnose 把 err 分类并给出可读原因 + 建议。
//
//   - err == nil → 返回零值（Category=unknown 但 Reason 空），调用方应避免；
//   - err 字符串会先过 SanitizeError（password / privateKey 等字面量遮蔽）；
//   - 内部按关键字匹配识别类别，多个命中时按"更具体优先"（auth > handshake > network）。
func Diagnose(err error) Diagnosis {
	if err == nil {
		return Diagnosis{Category: CatUnknown, Reason: "", Suggestion: ""}
	}
	raw := SanitizeError(err.Error())
	lower := strings.ToLower(raw)

	cat, reason, suggestion := classify(lower, err)

	return Diagnosis{
		Category:   cat,
		Reason:     reason,
		Suggestion: suggestion,
		Raw:        raw,
	}
}

// Categorize 仅返回类别（Diagnose 的简化版）。快捷使用。
func Categorize(err error) Category {
	if err == nil {
		return CatUnknown
	}
	cat, _, _ := classify(strings.ToLower(SanitizeError(err.Error())), err)
	return cat
}

// classify 按"更具体优先"顺序匹配关键字，返回 (类别, 原因, 建议)。
//
// 顺序原则：
//  1. ctx cancel / deadline（最具体，运营同学一看就知道是超时）；
//  2. host key 错误（要先于 handshake，避免被 KEX 字面量误判）；
//  3. auth / kbd-interactive（要按顺序，kbd 命中的关键字通常也含 auth 词）；
//  4. handshake（KEX 算法 / cipher 不匹配）；
//  5. DNS / 网络 / 端口（TCP 层错误）；
//  6. SFTP / Command；
//  7. 兜底 unknown。
func classify(lower string, orig error) (Category, string, string) {
	// 1. ctx 超时 / 取消
	if errors.Is(orig, context.Canceled) {
		return CatTimeout, "操作被取消", "检查前端是否触发了取消按钮，或者父 ctx 是否已过期"
	}
	if errors.Is(orig, context.DeadlineExceeded) {
		return CatTimeout, "连接超时", "检查网络可达性；老 sshd 可能在自动 fallback 多套算法时超时，可加大 sshAttemptTimeout"
	}
	if strings.Contains(lower, "i/o timeout") || strings.Contains(lower, "io timeout") {
		return CatTimeout, "网络 I/O 超时", "检查网络可达性 / 防火墙；老 sshd 自动 fallback 多套算法可能需要更长超时"
	}
	if strings.Contains(lower, "deadline exceeded") {
		return CatTimeout, "握手超时", "可调大 app.ssh_log_max_mb 或 ssh_compat_profile"
	}

	// 2. host key 相关
	if strings.Contains(lower, "host key") ||
		strings.Contains(lower, "known_hosts") ||
		strings.Contains(lower, "hostkey") ||
		strings.Contains(lower, "verification failed") {
		return CatHostKey, "host key 校验失败",
			"如果确认这台 server 是可信的，把 host key 指纹配到 server.host_key_sha256；" +
				"否则请检查是否被中间人攻击"
	}

	// 3. 认证类（password / publickey / kbd-interactive 都覆盖）
	if strings.Contains(lower, "keyboard-interactive failed") ||
		strings.Contains(lower, "no keyboard") {
		return CatKbdInt, "PAM / keyboard-interactive 认证失败",
			"服务端 PAM 配置可能不允许 password auth；可让运维改 sshd_config 的 KbdInteractiveAuthentication"
	}
	if strings.Contains(lower, "unable to authenticate") ||
		strings.Contains(lower, "no supported methods remain") ||
		strings.Contains(lower, "authentication failed") ||
		strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "too many authentication failures") {
		return CatAuth, "认证失败",
			"检查密码是否正确；账号是否被锁定；如果密码含特殊字符可能被 sshd 转义出错，可改用 SSH 私钥认证"
	}

	// 4. 协议握手 / 算法协商
	if strings.Contains(lower, "handshake failed") ||
		strings.Contains(lower, "no matching algorithms") ||
		strings.Contains(lower, "no common algorithms") ||
		strings.Contains(lower, "no mutually") ||
		strings.Contains(lower, "kex") ||
		strings.Contains(lower, "ssh: protocol error") {
		return CatHandshake, "SSH 握手失败（算法协商不匹配）",
			"通常是老 sshd / 厂商定制 OpenSSH 不支持现代算法；" +
				"可在 config.yaml 把 app.ssh_compat_profile 设为 compat / no-ecdh / legacy 重试"
	}

	// 5. TCP 层 — DNS / 网络 / 端口
	var dnsErr *net.DNSError
	if errors.As(orig, &dnsErr) {
		return CatDNS, "DNS 解析失败：" + dnsErr.Name,
			"检查主机名拼写；检查本机 / 内网 DNS 服务器是否可达"
	}
	if strings.Contains(lower, "no such host") {
		return CatDNS, "DNS 解析失败", "检查主机名拼写；检查 /etc/resolv.conf 或内网 DNS"
	}
	if strings.Contains(lower, "connection refused") {
		return CatPort, "端口拒绝连接", "确认 SSH 端口（默认 22）是否正确；sshd 是否启动"
	}
	if strings.Contains(lower, "connection reset") || strings.Contains(lower, "econnreset") {
		return CatNetwork, "TCP 连接被重置", "可能在握手阶段被防火墙 / 厂商补丁拦截；可加 ssh_compat_profile 重试"
	}
	if strings.Contains(lower, "no route to host") || strings.Contains(lower, "network is unreachable") {
		return CatNetwork, "无可达路由", "检查本机与目标机器之间的网络（VLAN / 防火墙 / 路由表）"
	}
	if strings.Contains(lower, "network is down") || strings.Contains(lower, "broken pipe") {
		return CatNetwork, "网络中断", "网络层连接断开，重试即可"
	}

	// 6. SFTP / Command
	if strings.Contains(lower, "sftp") && (strings.Contains(lower, "subsystem") ||
		strings.Contains(lower, "failed") || strings.Contains(lower, "request")) {
		return CatSFTP, "SFTP 子系统失败",
			"服务端 sshd_config 可能没启用 Subsystem sftp；可改用 ShellBackend fallback（list_mode=posix_ls）"
	}
	if strings.Contains(lower, "exit status") || strings.Contains(lower, "command") {
		return CatCommand, "远程命令执行失败", "检查远端命令是否合法（命令 / 路径 / 权限）"
	}

	// 兜底：能拿到 net.Error 但上面没匹配，标记网络类
	var nerr net.Error
	if errors.As(orig, &nerr) {
		if nerr.Timeout() {
			return CatTimeout, "网络操作超时", "重试或调大超时"
		}
		return CatNetwork, "网络错误", "检查网络连通性"
	}

	// 完全未知（保留 raw 给运维排查）
	_ = time.Now()
	return CatUnknown, "未知 SSH 错误", "把 raw 字段贴给运维，或查看 logs/ssh_debug.log 找原因"
}
