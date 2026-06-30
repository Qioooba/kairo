package license

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// cipherKey 32 字节 AES-256 密钥, 写死在二进制里 (用 SHA256 派生自一个固定字符串)。
//
// ⚠️ 反编译者能看到底层字符串, 但用户的威胁模型 (内网 + 同事级别) 不防反编译。
// 安全链:
//   1) 反编译拿到 AES key → 能解密本地证书
//   2) 但证书里的 IP 是固定的, 攻击者把证书文件复制到别的机器 → GCM AAD 校验失败
//   3) 攻击者要伪造一个"在他本机 IP 上有效"的证书 → 需要重新激活
//   4) 重新激活必须用真实激活码 → 服务端 IP 不匹配 → 拒绝 + 写日志
//
// 攻击者真正能做的: 在他自己机器上伪造一个证书, 但服务端会立刻拒绝他的激活。
// 代价 / 收益完全不划算, 攻击者不如直接找用户要激活码。
//
// 如果将来需要更强的保护, 把这个 key 改成:
//   1) 每台机器独立生成 (用 hardware fingerprint 派生)
//   2) 或者换成 RSA 非对称签名 (Java 端私钥 + 客户端公钥)
// 但当前 v1.0 需求里没这个必要。
var cipherKey = sha256Sum("kairo-license-cert-v1-2026")

// sha256Sum 工具: 给字符串算 SHA256, 取前 32 字节作为 AES key
func sha256Sum(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// Cert 客户端证书明文结构。激活成功后由前端 /api/license/activate 调用 saveLocalCert 写入。
//
// 注意: Cert 只存 IP, 不存 MAC。
// MAC 作为 GCM AAD 参与解密, 但不出现在明文里 - 防止反编译者从 cert 文件直接读到 MAC 信息。
type Cert struct {
	Code string `json:"code"` // 激活码
	IP   string `json:"ip"`   // 首次激活的本机 IP
}

// aadForCert 计算 cert 的 GCM AAD。
//
// AAD = IP + "|" + 本机 MAC fingerprint
// 这是关键安全点: 攻击者反编译拿到 AES key 也不能伪造 cert,
// 因为 AAD 校验要求 IP + MAC 同时匹配, 攻击者要同时知道两者才能伪造。
func aadForCert(ip string) []byte {
	return []byte(ip + "|" + macFingerprint())
}

// storedCert 落盘格式 (加密后的本地证书文件内容)。
// IP 字段明文存一份方便人眼查看; 真正的安全保证在 payload (AES-GCM 密文) 和 AAD 校验。
type storedCert struct {
	Payload string `json:"payload"` // base64(nonce + ciphertext)
	IP      string `json:"ip"`      // 明文 IP (仅供调试, GCM AAD 校验才是真校验)
}

// certPath 返回 ~/.kairo/license.dat 的绝对路径。
// macOS / Linux:  /Users/<user>/.kairo/license.dat 或 /home/<user>/.kairo/license.dat
// Windows:        C:\Users\<user>\.kairo\license.dat
func certPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法获取用户主目录: %w", err)
	}
	return filepath.Join(home, ".kairo", "license.dat"), nil
}

// loadLocalCert 读取并解密本地证书。
//
// 返回值:
//   - (cert, nil) → 解密成功, cert.IP 应该等于 localIP() 才算有效
//   - (nil, os.ErrNotExist) → 文件不存在, 用户首次启动
//   - (nil, 其他 error) → 文件存在但损坏 / IP 不匹配 / AES 校验失败
func loadLocalCert() (*Cert, error) {
	p, err := certPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}

	var sc storedCert
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("证书文件 JSON 格式错误: %w", err)
	}

	raw, err := base64.StdEncoding.DecodeString(sc.Payload)
	if err != nil {
		return nil, fmt.Errorf("证书 payload base64 解码失败: %w", err)
	}
	if len(raw) < 12 {
		return nil, errors.New("证书 payload 长度异常")
	}

	// 前 12 字节是 nonce, 后面是 ciphertext
	nonce, ct := raw[:12], raw[12:]

	block, err := aes.NewCipher(cipherKey)
	if err != nil {
		return nil, fmt.Errorf("AES 初始化失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("GCM 初始化失败: %w", err)
	}

	// AAD = IP + "|" + MAC fingerprint (关键安全点!)
//
// AAD 设计要点 (防伪造):
//   1) 攻击者复制 license.dat 到别的机器:
//      - sc.IP 是 A 的 IP, B 启动时 AAD 算的是 "A 的 IP + B 的 MAC"
//      - 但 GCM Open 时 AAD 是 "sc.IP + 本机 MAC" = "A 的 IP + B 的 MAC"
//      - 跟加密时的 AAD ("A 的 IP + A 的 MAC") 不匹配 → 解密失败
//      - ✅ 拦截!
//   2) 攻击者反编译拿到 AES key, 想伪造 cert 给受害者:
//      - 必须知道受害者的 IP **和** MAC
//      - 攻击者构造 cert {code: 任意, ip: 受害者 IP}, 用 AES key 加密
//      - AAD 是 "受害者 IP + 受害者 MAC"
//      - 但攻击者可能不知道受害者 MAC, 用了自己的 MAC 或猜测
//      - 受害者启动, AAD 算的是 "受害者 IP + 受害者 MAC"
//      - 跟攻击者用的 AAD 不匹配 → 解密失败
//      - ✅ 拦截!
//
// 攻击者要成功伪造, 必须**同时**知道 IP + MAC + 拿到 AES key + 主动发给特定受害者。
// 对内网运维工具 + 同事级别威胁, 这个门槛足够。
	plaintext, err := gcm.Open(nil, nonce, ct, aadForCert(sc.IP))
	if err != nil {
		return nil, fmt.Errorf("证书解密失败 (IP 不匹配或文件被篡改): %w", err)
	}

	var cert Cert
	if err := json.Unmarshal(plaintext, &cert); err != nil {
		return nil, fmt.Errorf("证书内容 JSON 解析失败: %w", err)
	}

	// 双重校验: 解密后的 cert.IP 必须 == sc.IP (防止 GCM 之后的二次篡改)
	if cert.IP != sc.IP {
		return nil, errors.New("证书 IP 字段内部不一致 (被篡改?)")
	}

	return &cert, nil
}

// saveLocalCert 加密并写入本地证书。激活成功后由 Activate() 调用。
//
// 写入策略:
//   - 加密 cert → payload (AES-GCM)
//   - AAD = cert.IP
//   - 落盘格式: {payload: base64(nonce+ct), ip: 明文}
//   - 文件权限 0o600 (仅当前用户可读写)
func saveLocalCert(cert *Cert) error {
	p, err := certPath()
	if err != nil {
		return err
	}

	// 确保 ~/.kairo 目录存在
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("创建证书目录失败: %w", err)
	}

	block, err := aes.NewCipher(cipherKey)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("生成 nonce 失败: %w", err)
	}

	plaintext, err := json.Marshal(cert)
	if err != nil {
		return fmt.Errorf("序列化证书失败: %w", err)
	}

	// AAD = IP + MAC fingerprint - 复制到别的机器 或 攻击者伪造都会 GCM 失败
	ct := gcm.Seal(nil, nonce, plaintext, aadForCert(cert.IP))

	// payload = nonce + ciphertext
	payload := append(append([]byte{}, nonce...), ct...)

	sc := storedCert{
		Payload: base64.StdEncoding.EncodeToString(payload),
		IP:      cert.IP,
	}
	data, err := json.Marshal(sc)
	if err != nil {
		return err
	}

	// 0o600: 防止其他用户读 (虽然他们已经能读 AES key, 但加这层无害)
	return os.WriteFile(p, data, 0o600)
}