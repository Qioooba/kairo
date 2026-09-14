package dbconsole

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	lobTokenSecretOnce sync.Once
	lobTokenSecret     []byte
)

func getLOBTokenSecret() []byte {
	lobTokenSecretOnce.Do(func() {
		lobTokenSecret = make([]byte, 32)
		if _, err := rand.Read(lobTokenSecret); err != nil {
			panic(fmt.Errorf("initialize LOB signing key: %w", err))
		}
	})
	return lobTokenSecret
}

// SignedLOBTokenPayload 结论 2.6: LOB 下载 Token 包含完整防篡改上下文信息
type SignedLOBTokenPayload struct {
	SourceID          string            `json:"sid"`
	SourceFingerprint string            `json:"sfp,omitempty"`
	DatabaseUser      string            `json:"usr,omitempty"`
	Owner             string            `json:"own"`
	Table             string            `json:"tbl"`
	Column            string            `json:"col"`
	ColumnType        string            `json:"ctp,omitempty"`
	RowID             string            `json:"rid,omitempty"`
	UseRowID          bool              `json:"urid,omitempty"`
	PrimaryKey        []string          `json:"pks,omitempty"`
	Keys              map[string]any    `json:"kys,omitempty"`
	KeyTypes          map[string]string `json:"kty,omitempty"`
	SessionID         string            `json:"ses,omitempty"`
	ExpiresAt         int64             `json:"exp"`
}

// GenerateSignedLOBTokenWithFingerprint 生成一个防篡改、短期有效且绑定数据源配置指纹的 LOB 下载 Token
func GenerateSignedLOBTokenWithFingerprint(sourceID, sourceFingerprint, dbUser, owner, table, column, colType, rowid string, keys map[string]any, pks []string, sessionID string, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	keyValues := make(map[string]any, len(keys))
	keyTypes := make(map[string]string)
	for name, value := range keys {
		switch v := value.(type) {
		case []byte:
			keyValues[name], keyTypes[name] = base64.StdEncoding.EncodeToString(v), "bytes"
		case time.Time:
			keyValues[name], keyTypes[name] = v.Format(time.RFC3339Nano), "time"
		default:
			keyValues[name] = value
		}
	}
	payload := SignedLOBTokenPayload{
		SourceID:          sourceID,
		SourceFingerprint: sourceFingerprint,
		DatabaseUser:      dbUser,
		Owner:             owner,
		Table:             table,
		Column:            column,
		ColumnType:        colType,
		RowID:             rowid,
		UseRowID:          len(pks) == 0 && strings.TrimSpace(rowid) != "",
		PrimaryKey:        pks,
		Keys:              keyValues,
		KeyTypes:          keyTypes,
		SessionID:         sessionID,
		ExpiresAt:         time.Now().Add(ttl).Unix(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal lob payload: %w", err)
	}

	payloadB64 := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, getLOBTokenSecret())
	mac.Write([]byte(payloadB64))
	sigHex := hex.EncodeToString(mac.Sum(nil))

	return payloadB64 + "." + sigHex, nil
}

// GenerateSignedLOBToken 生成一个防篡改、短期有效的 LOB 下载 Token (向后兼容)
func GenerateSignedLOBToken(sourceID, dbUser, owner, table, column, colType, rowid string, keys map[string]any, pks []string, sessionID string, ttl time.Duration) (string, error) {
	return GenerateSignedLOBTokenWithFingerprint(sourceID, "", dbUser, owner, table, column, colType, rowid, keys, pks, sessionID, ttl)
}

// VerifySignedLOBToken 校验 Token 签名与有效期，并反序列化为 LOBRef 与 SourceID
func VerifySignedLOBToken(token string) (*SignedLOBTokenPayload, *LOBRef, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil, errors.New("token 不能为空")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, nil, errors.New("token 格式非法")
	}

	payloadB64, sigHex := parts[0], parts[1]
	expectedSig, err := hex.DecodeString(sigHex)
	if err != nil {
		return nil, nil, errors.New("token 签名编码非法")
	}

	mac := hmac.New(sha256.New, getLOBTokenSecret())
	mac.Write([]byte(payloadB64))
	actualSig := mac.Sum(nil)

	if !hmac.Equal(expectedSig, actualSig) {
		return nil, nil, errors.New("token 签名校验失败（内容已被篡改）")
	}

	data, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, nil, fmt.Errorf("token payload 解码失败: %w", err)
	}

	var payload SignedLOBTokenPayload
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, nil, fmt.Errorf("token payload 反序列化失败: %w", err)
	}

	if time.Now().Unix() > payload.ExpiresAt {
		return nil, nil, errors.New("token 已过期，请刷新结果重新获取")
	}
	for name, typ := range payload.KeyTypes {
		value, ok := payload.Keys[name].(string)
		if !ok {
			return nil, nil, errors.New("LOB 主键类型无效")
		}
		switch typ {
		case "bytes":
			decoded, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				return nil, nil, err
			}
			payload.Keys[name] = decoded
		case "time":
			decoded, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return nil, nil, err
			}
			payload.Keys[name] = decoded
		default:
			return nil, nil, errors.New("LOB 主键类型不受支持")
		}
	}

	ref := &LOBRef{
		Owner:      payload.Owner,
		Table:      payload.Table,
		Column:     payload.Column,
		ColumnType: payload.ColumnType,
		RowID:      payload.RowID,
		UseRowID:   payload.UseRowID,
		PrimaryKey: payload.PrimaryKey,
		Keys:       payload.Keys,
		SessionID:  payload.SessionID,
	}

	return &payload, ref, nil
}
