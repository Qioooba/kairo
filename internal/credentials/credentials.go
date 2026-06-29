// Package credentials 凭据存储后端，支持三种模式：
//   - keyring（默认）→ macOS Keychain / Windows DPAPI / Linux Secret Service
//   - file           → AES-GCM 加密本地文件 (data/credentials.json)
//   - disabled       → 不持久化密码，每次都需要用户手动输入
//
// 设计要点：
//   - Service 固定为 "DoubaoToolbox"，account 编码为 "system|server|username"；
//   - 永远不会把密码写进 audit.log、URL、错误信息或前端响应；
//   - keyring 不可用时返回 ErrUnavailable，前端提示用户"无法访问系统钥匙串"；
//   - file 模式使用 AES-256-GCM 加密，密钥从 credential_key 配置读取，
//     未配置则自动生成并保存到 data/.credkey（仅当前用户可读 0600）。
package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

const Service = "DoubaoToolbox"

const (
	ModeKeyring  = "keyring"
	ModeFile     = "file"
	ModeDisabled = "disabled"
)

var (
	ErrUnavailable = errors.New("credentials: 凭据存储不可用")
	ErrNotSaved    = errors.New("credentials: 未保存密码")
)

var (
	modeMu sync.RWMutex
	mode   = ModeKeyring
)

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

func Mode() string {
	modeMu.RLock()
	defer modeMu.RUnlock()
	return mode
}

func guardMode() error {
	return guardModeValue(Mode())
}

func guardModeValue(m string) error {
	switch m {
	case ModeDisabled:
		return fmt.Errorf("%w（配置 credential_store=disabled）", ErrUnavailable)
	default:
		return nil
	}
}

func Key(system, server, username string) string {
	return strings.Join([]string{system, server, username}, "|")
}

// ---------- file backend ----------

var (
	fileMu      sync.Mutex
	fileInit    bool
	fileDataDir string
	fileKey     []byte
	filePath    string
)

// encryptedEntry 存储在 JSON 里的密文条目
type encryptedEntry struct {
	Nonce      string `json:"n"` // hex
	Ciphertext string `json:"c"` // hex
}

// Init 初始化 file 后端（必须在 SetMode 之后、使用凭据功能之前调用）。
//
//   - dataDir: 数据目录绝对路径（cfg.DataDir()）
//   - configuredKey: 配置文件中的 credential_key（hex 编码 64 字符），空字符串表示自动生成
//
// 如果当前 mode 不是 file，Init 是 no-op。
func Init(dataDir, configuredKey string) error {
	modeMu.RLock()
	m := mode
	modeMu.RUnlock()

	if m != ModeFile {
		fileMu.Lock()
		fileInit = false
		fileMu.Unlock()
		return nil
	}

	fileMu.Lock()
	defer fileMu.Unlock()

	fileDataDir = dataDir
	filePath = filepath.Join(dataDir, "credentials.json")

	var key []byte
	if strings.TrimSpace(configuredKey) != "" {
		k, err := hex.DecodeString(strings.TrimSpace(configuredKey))
		if err != nil {
			return fmt.Errorf("credentials: credential_key hex 解码失败: %w", err)
		}
		if len(k) != 32 {
			return fmt.Errorf("credentials: credential_key 长度必须为 32 字节（64 hex 字符），当前 %d 字节", len(k))
		}
		key = k
	} else {
		credKeyPath := filepath.Join(dataDir, ".credkey")
		k, err := os.ReadFile(credKeyPath)
		if err == nil {
			kStr := strings.TrimSpace(string(k))
			decoded, derr := hex.DecodeString(kStr)
			if derr == nil && len(decoded) == 32 {
				key = decoded
			} else {
				if err := os.Remove(credKeyPath); err != nil {
					return fmt.Errorf("credentials: 旧 .credkey 损坏且删除失败: %w", err)
				}
			}
		}
		if key == nil {
			key = make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return fmt.Errorf("credentials: 生成随机密钥失败: %w", err)
			}
			if err := os.MkdirAll(dataDir, 0o755); err != nil {
				return fmt.Errorf("credentials: 创建 data 目录失败: %w", err)
			}
			hexKey := hex.EncodeToString(key)
			if err := os.WriteFile(credKeyPath, []byte(hexKey), 0o600); err != nil {
				return fmt.Errorf("credentials: 写入 .credkey 失败: %w", err)
			}
		}
	}

	fileKey = key
	fileInit = true

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("credentials: 创建 data 目录失败: %w", err)
	}

	return nil
}

func fileReady() error {
	fileMu.Lock()
	defer fileMu.Unlock()
	if !fileInit || len(fileKey) != 32 {
		return fmt.Errorf("%w: file 后端未初始化（请调 credentials.Init）", ErrUnavailable)
	}
	return nil
}

func encrypt(key, plaintext string) (*encryptedEntry, error) {
	block, err := aes.NewCipher(fileKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), []byte(key))
	return &encryptedEntry{
		Nonce:      hex.EncodeToString(nonce),
		Ciphertext: hex.EncodeToString(ct),
	}, nil
}

// decrypt 解密一条密文。返回 (明文, isLegacy, error)。
// isLegacy=true 表示命中的是未绑定 AAD 的旧格式密文（向后兼容路径），
// 调用方应据此触发一次性迁移到新格式（见 BE-013）。
func decrypt(key string, e *encryptedEntry) (string, bool, error) {
	nonce, err := hex.DecodeString(e.Nonce)
	if err != nil {
		return "", false, fmt.Errorf("credentials: nonce hex 解码失败: %w", err)
	}
	ct, err := hex.DecodeString(e.Ciphertext)
	if err != nil {
		return "", false, fmt.Errorf("credentials: ciphertext hex 解码失败: %w", err)
	}
	block, err := aes.NewCipher(fileKey)
	if err != nil {
		return "", false, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", false, err
	}
	if len(nonce) != gcm.NonceSize() {
		return "", false, errors.New("credentials: nonce 长度不正确")
	}
	// BE-013：优先用 AAD 绑定格式解密；失败后再按旧格式（nil AAD）读取，
	// 命中旧格式时返回 isLegacy=true 让调用方触发迁移重写，逐步消除旧密文。
	pt, err := gcm.Open(nil, nonce, ct, []byte(key))
	if err != nil {
		pt, err = gcm.Open(nil, nonce, ct, nil)
		if err != nil {
			return "", false, fmt.Errorf("credentials: 解密失败（密钥不匹配或数据损坏）: %w", err)
		}
		return string(pt), true, nil
	}
	return string(pt), false, nil
}

func loadFile() (map[string]encryptedEntry, error) {
	entries := make(map[string]encryptedEntry)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return entries, nil
		}
		return nil, fmt.Errorf("credentials: 读取凭据文件失败: %w", err)
	}
	if len(data) == 0 {
		return entries, nil
	}
	var raw map[string]encryptedEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("credentials: 解析凭据文件失败: %w", err)
	}
	if raw != nil {
		entries = raw
	}
	return entries, nil
}

func saveFile(entries map[string]encryptedEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("credentials: 序列化凭据失败: %w", err)
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(filePath), ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("credentials: 创建临时文件失败: %w", err)
	}
	tmp := tmpFile.Name()
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("credentials: 设置临时文件权限失败: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("credentials: 写入凭据文件失败: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("credentials: 关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, filePath); err != nil {
		// Windows 上 os.Rename 可能因杀毒软件/备份软件实时扫描目标文件而偶发失败
		// （Access is denied / Permission denied）。sleep 100ms 让扫描完成后重试一次，
		// 绕过这种瞬时占用；其它平台 rename 失败就是真失败，不重试。
		if runtime.GOOS == "windows" {
			time.Sleep(100 * time.Millisecond)
			if err2 := os.Rename(tmp, filePath); err2 == nil {
				return nil
			}
		}
		_ = os.Remove(tmp)
		return fmt.Errorf("credentials: 替换凭据文件失败: %w", err)
	}
	return nil
}

func fileSave(key, password string) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	entries, err := loadFile()
	if err != nil {
		return err
	}
	enc, err := encrypt(key, password)
	if err != nil {
		return fmt.Errorf("credentials: 加密失败: %w", err)
	}
	entries[key] = *enc
	return saveFile(entries)
}

func fileGet(key string) (string, error) {
	fileMu.Lock()
	defer fileMu.Unlock()

	entries, err := loadFile()
	if err != nil {
		return "", err
	}
	e, ok := entries[key]
	if !ok {
		return "", ErrNotSaved
	}
	pt, isLegacy, err := decrypt(key, &e)
	if err != nil {
		return "", err
	}
	// BE-013：读取到旧格式（未绑定 AAD）密文时，立即用新格式重写存储，
	// 一次性迁移；下次读取即走 AAD 校验路径。迁移失败仅记日志不阻塞读取。
	if isLegacy {
		if enc, mErr := encrypt(key, pt); mErr == nil {
			entries[key] = *enc
			_ = saveFile(entries)
		}
	}
	return pt, nil
}

func fileClear(key string) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	entries, err := loadFile()
	if err != nil {
		return err
	}
	if _, ok := entries[key]; !ok {
		return ErrNotSaved
	}
	delete(entries, key)
	return saveFile(entries)
}

// ---------- public API ----------

func Save(system, server, username, password string) error {
	if system == "" || server == "" || username == "" {
		return errors.New("credentials: system/server/username 不能为空")
	}
	if password == "" {
		return errors.New("credentials: 密码不能为空")
	}
	currentMode := Mode()
	if err := guardModeValue(currentMode); err != nil {
		return err
	}
	k := Key(system, server, username)
	if currentMode == ModeFile {
		if err := fileReady(); err != nil {
			return err
		}
		return fileSave(k, password)
	}
	if err := keyring.Set(Service, k, password); err != nil {
		if isUnavailable(err) {
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return fmt.Errorf("credentials: 写入钥匙串失败: %w", err)
	}
	return nil
}

func Get(system, server, username string) (string, error) {
	if system == "" || server == "" || username == "" {
		return "", errors.New("credentials: system/server/username 不能为空")
	}
	currentMode := Mode()
	if err := guardModeValue(currentMode); err != nil {
		return "", err
	}
	k := Key(system, server, username)
	if currentMode == ModeFile {
		if err := fileReady(); err != nil {
			return "", err
		}
		return fileGet(k)
	}
	pw, err := keyring.Get(Service, k)
	if err != nil {
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

func Clear(system, server, username string) error {
	if system == "" || server == "" || username == "" {
		return errors.New("credentials: system/server/username 不能为空")
	}
	currentMode := Mode()
	if err := guardModeValue(currentMode); err != nil {
		return err
	}
	k := Key(system, server, username)
	if currentMode == ModeFile {
		if err := fileReady(); err != nil {
			return err
		}
		return fileClear(k)
	}
	if err := keyring.Delete(Service, k); err != nil {
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

func isUnavailable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, hint := range []string{
		"dbus", "secret service", "no such interface",
		"could not connect", "the user name or password is incorrect",
		"keychain", "errse", "access denied",
		"org.freedesktop.secrets", "collection",
		"exit status", "permission denied", "sandbox",
		"restricted", "not allowed",
	} {
		if strings.Contains(s, hint) {
			return true
		}
	}
	return false
}
