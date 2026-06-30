package license

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
)

// localIP 取本机第一个非 loopback 的 IPv4 地址。
//
// 简化策略: 遍历所有网卡, 跳过 loopback, 取第一个 IPv4。
//
// 边界:
//   - 多网卡机器 (有线 + 无线 + VPN) → 取第一个, 不一定是用户预期的 IP。
//     对内网固定 IP 的运维同事, 实际机器一般只有一个网卡, 这不是问题。
//   - 取不到时 fallback 到 "127.0.0.1", 此时本地证书校验会用 127.0.0.1 比对,
//     不太可能匹配 → 用户会走激活流程重新绑定, 这是安全兜底。
//   - Windows + WSL / Docker 场景会拿到奇怪的 IP, 用户激活时绑的就是这个 "本机 IP"。
func localIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if v4 := ipnet.IP.To4(); v4 != nil {
				return v4.String()
			}
		}
	}
	return "127.0.0.1"
}

// macFingerprint 取本机第一个非 loopback 网卡的 MAC 地址, SHA256 后取前 16 字节 hex。
//
// 用途: 作为本地证书的额外 AAD, 防止攻击者"猜对 IP 后伪造证书"。
// 安全模型:
//   - 攻击者要伪造 cert, 必须知道 IP + MAC
//   - IP 通过 ARP 扫描 / OUI 表能拿到
//   - MAC 通过 ARP 扫描也能拿到 (任意内网机器)
//   - 但要**同时**知道 IP + MAC + 拿到 AES key + 主动发给特定受害者, 成本极高
//   - 内网同事级别威胁下, 这个组合条件足以拦住攻击
//
// 取不到 MAC 时 (罕见: 无网卡 / 权限问题) → 返回固定 fallback, 仍然能保护 IP 维度。
func macFingerprint() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "nomac-fallback"
	}
	for _, iface := range ifaces {
		// 跳过 loopback 和没 MAC 的虚拟接口
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(iface.HardwareAddr) == 0 {
			continue
		}
		// 取第一个非 loopback 有 MAC 的网卡
		h := sha256.Sum256([]byte(iface.HardwareAddr.String()))
		return hex.EncodeToString(h[:8]) // 16 字符 hex
	}
	return "nomac-fallback"
}

// Fingerprint 返回 IP + MAC 拼接的指纹 (AAD 用)。
func Fingerprint() string {
	return localIP() + "|" + macFingerprint()
}