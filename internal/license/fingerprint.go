package license

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
)

// primaryNetInfo 从同一张网卡取 IPv4 地址和 MAC 指纹。
//
// 旧版 localIP() 用 net.InterfaceAddrs() 遍历地址, macFingerprint() 用
// net.Interfaces() 遍历网卡, 两者独立迭代 → 多网卡机器上可能 IP 来自 eth0
// 而 MAC 来自 docker0, AAD 出现 IP-A|MAC-B 的错配, 重启后接口顺序变化
// 会导致 AAD 漂移 → 强制重新激活。
//
// 修复: 以 net.Interfaces() 为唯一数据源, 找到第一张同时有 MAC 和非 loopback
// IPv4 的网卡, IP 和 MAC 都从它取。这样 AAD 始终是同一张网卡的 IP|MAC,
// 不会因两套迭代顺序不一致而漂移。
//
// 边界:
//   - 找不到"同时有 IP + MAC"的网卡 (如纯 VPN 隧道, 有 IP 无 MAC):
//     IP 走 net.InterfaceAddrs() 兜底, MAC 用 "nomac-fallback"。
//     AAD 退化为 IP-only 维度, 仍有 IP 绑定保护。
//   - 取不到任何 IP → "127.0.0.1", 不会匹配已有证书 → 走激活流程。
func primaryNetInfo() (ip, macFP string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1", "nomac-fallback"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(iface.HardwareAddr) == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if v4 := ipnet.IP.To4(); v4 != nil {
					h := sha256.Sum256([]byte(iface.HardwareAddr.String()))
					return v4.String(), hex.EncodeToString(h[:8])
				}
			}
		}
	}
	// 兜底: 没有"同时有 IP + MAC"的网卡, 尝试只取 IP (VPN-only 等场景)
	return fallbackLocalIP(), "nomac-fallback"
}

// fallbackLocalIP 用 net.InterfaceAddrs 取第一个非 loopback IPv4 (兜底路径)。
func fallbackLocalIP() string {
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

func localIP() string {
	ip, _ := primaryNetInfo()
	return ip
}

func macFingerprint() string {
	_, mac := primaryNetInfo()
	return mac
}

// Fingerprint 返回 IP + MAC 拼接的指纹 (AAD 用)。
func Fingerprint() string {
	return localIP() + "|" + macFingerprint()
}
