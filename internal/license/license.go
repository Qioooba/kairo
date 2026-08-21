// Package license 提供 Kairo 工具箱的本地激活控制。
//
// 整体设计（v1.0，按用户拍板）：
//
//   - 启动顺序: 开发者白名单 → 本地证书 → 弹激活窗
//   - 本地证书用 AES-GCM 加密存, AAD 绑 IP, 防止复制到别的机器后被使用
//   - 服务端只校验"激活码 ↔ IP", 客户端证书自管 (不依赖服务端私钥签名)
//   - 整个流程不依赖外部基础服务 (keyring / 中心授权), 只靠:
//       1) 客户端二进制里写死的 AES 密钥 (反编译不在防护范围)
//       2) 服务端"激活码 ↔ IP 绑定" (IP 改了立刻拒绝)
//
// 不防什么:
//   - 反编译 Go 二进制拿到 AES key (用户接受这个风险)
//   - 直接修改二进制跳过所有检查 (成本太高, 攻击者不如直接找用户要激活码)
package license

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// ConfigProvider 由 main.go 在启动时注入, 用于在 license 校验时读取当前配置。
// 设置之后所有 license 操作都会用这个 provider 拿最新配置。
type ConfigProvider func() *ConfigSnapshot

// ConfigSnapshot 是 license 包关心的配置子集, 解耦避免循环引用 internal/config。
// main.go 在注入时把 config.AppConfig.KairoInternalToken + config.InternalEndpoints
// 拷贝到本结构里。
type ConfigSnapshot struct {
	KairoInternalToken string
	// v0.14: 激活服务 endpoint 配置 (主备 + auth). 注入后立即用这些值覆盖 var 池
	// (LicenseServerPrimary / LicenseServerSecondary / BasicAuthHeader), 留空则不动 var.
	LicenseActivatePrimary   string
	LicenseActivateSecondary string
	LicenseActivateAuth      string
}

var (
	cfgProviderMu sync.RWMutex
	cfgProvider   ConfigProvider
)

// SetConfigProvider 由 main.go 启动时调用一次, 注入能拿当前 config 的函数。
// 函数应返回 *ConfigSnapshot, license 内部用它的字段覆盖 var 池 / 提供 dev-bypass 校验。
//
// 注入后立刻调一次 provider 拿 snapshot 并应用 (覆盖 var 池) —— 这样测试代码不调
// SetConfigProvider 时, var 池保持源码默认值, 老测试一行不动也能跑。
func SetConfigProvider(f ConfigProvider) {
	cfgProviderMu.Lock()
	defer cfgProviderMu.Unlock()
	cfgProvider = f
	snap := f()
	applySnapshotLocked(snap)
}

// applySnapshotLocked 把 snapshot 里的值覆盖到 var 池。
// 调用方必须持有 cfgProviderMu 写锁, 避免 SetConfigProvider 并发时被穿插。
// snap 为 nil 或字段为空, 不覆盖 (保留原 var 值)。
func applySnapshotLocked(snap *ConfigSnapshot) {
	if snap == nil {
		return
	}
	if snap.LicenseActivatePrimary != "" {
		LicenseServerPrimary = snap.LicenseActivatePrimary
	}
	if snap.LicenseActivateSecondary != "" {
		LicenseServerSecondary = snap.LicenseActivateSecondary
	}
	if snap.LicenseActivateAuth != "" {
		BasicAuthHeader = snap.LicenseActivateAuth
	}
}

// ErrLicenseMissing 本地证书缺失 / 失效 / IP 不匹配, 需要走激活流程。
var ErrLicenseMissing = errors.New("license missing or invalid")

// ErrActivateFailed 激活请求失败 (服务端拒绝 / 网络不通)。
var ErrActivateFailed = errors.New("activate failed")

// Status 当前 license 状态, 供前端 /api/license/status 查询。
type Status struct {
	Licensed bool   `json:"licensed"`
	Reason   string `json:"reason,omitempty"` // dev-bypass / local-cert / no-local-cert / ip-mismatch / cert-corrupted
}

// Check 启动时 (或前端轮询时) 调用。
//
// 检查顺序 (按用户要求):
//   1) 开发者白名单 - config.yaml 里的 kairo: "111222" 匹配 → 直接通过
//   2) 本地证书     - ~/.kairo/license.dat 用写死的 AES key 解密, IP 匹配 → 通过
//   3) 否则         - 返回 ErrLicenseMissing, 前端弹激活窗
//
// 返回 nil 表示通过, 返回 ErrLicenseMissing 或其他错误表示未通过。
func Check() error {
	if devBypass() {
		return nil
	}

	cert, err := loadLocalCert()
	if err == nil && cert != nil && cert.IP == localIP() {
		return nil
	}
	return ErrLicenseMissing
}

// GetStatus 返回详细状态, 给前端做 UI 区分 (比如不同 reason 显示不同提示)。
func GetStatus() Status {
	if devBypass() {
		return Status{Licensed: true, Reason: "dev-bypass"}
	}
	cert, err := loadLocalCert()
	if err == nil && cert != nil && cert.IP == localIP() {
		return Status{Licensed: true, Reason: "local-cert"}
	}
	return Status{Licensed: false, Reason: classifyFailReason(cert, err)}
}

// CurrentCode 返回本地证书中记录的激活码（仅证书有效时）。
// 用于派生稳定的宠物标识：同一激活码永远对应同一只宠物，
// 防止用户重装 / 删 pet.json 后生成新宠物 ID，在排行榜上出现多个"自己"。
func CurrentCode() (string, bool) {
	cert, err := loadLocalCert()
	if err != nil || cert == nil {
		return "", false
	}
	code := strings.TrimSpace(cert.Code)
	if code == "" {
		return "", false
	}
	return code, true
}

// Activate 由前端 /api/license/activate 端点调用, 流程:
//   1) POST {secret_key=激活码, ip=本机IP} 到 Java 服务端
//   2) 成功 → 加密写本地证书 → 返回 nil
//   3) 失败 → 把服务端 error 返回给前端展示
//
// 主地址失败自动试备用地址, 全失败才报错。
func Activate(code string) error {
	if code == "" {
		return fmt.Errorf("%w: 激活码不能为空", ErrActivateFailed)
	}
	ip := localIP()
	resp, err := callActivate(code, ip)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrActivateFailed, err)
	}
	if !resp.OK {
		return fmt.Errorf("%w: %s", ErrActivateFailed, resp.Error)
	}
	if err := saveLocalCert(&Cert{Code: code, IP: ip}); err != nil {
		return fmt.Errorf("激活成功但保存证书失败: %w", err)
	}
	return nil
}

// classifyFailReason 区分失败原因, 给前端展示不同提示。
func classifyFailReason(cert *Cert, err error) string {
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "no-local-cert"
		}
		return "cert-corrupted"
	}
	if cert == nil {
		return "no-local-cert"
	}
	if cert.IP != localIP() {
		return "ip-mismatch"
	}
	return "unknown"
}

// getConfigSnapshot 拿当前配置的快照 (内部用)。
func getConfigSnapshot() *ConfigSnapshot {
	cfgProviderMu.RLock()
	defer cfgProviderMu.RUnlock()
	if cfgProvider == nil {
		return nil
	}
	return cfgProvider()
}

// InitFromConfig 从注入的 ConfigProvider 拿最新 snapshot 并覆盖 var 池。
// 调用场景: cfg 热替换后 (config.Manager.Replace) 想让激活地址立刻生效, 调一次。
// 没注入 provider 时 (测试) 是 no-op, 不影响 var 池。
func InitFromConfig() {
	cfgProviderMu.Lock()
	defer cfgProviderMu.Unlock()
	if cfgProvider == nil {
		return
	}
	applySnapshotLocked(cfgProvider())
}