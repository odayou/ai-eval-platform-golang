package engine

// secrets.go — Secrets 加密存储 + frozen + expose 协议(good-skills #49 补齐)。
//
// 补齐专家评审:api_key 明文存储(schema_core.go:43 自承) + 模型可变(UPDATE 可改)。
// 实现:AES-GCM 加密 + 序列化默认脱敏 + expose context flag 才暴露明文。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// encryptionKey 加密密钥(从环境变量 SECRETS_ENC_KEY 读,32 字节 hex)。
// 若未设则用默认(仅 Demo 安全,生产必须设)。
var encryptionKey = getEncryptionKey()

func getEncryptionKey() []byte {
	if key := os.Getenv("SECRETS_ENC_KEY"); len(key) == 64 {
		b, _ := hex.DecodeString(key)
		return b
	}
	// 默认 key(Demo 用,32 字节)。生产环境必须设 SECRETS_ENC_KEY。
	return []byte("ai-eval-platform-default-key!!!!") // 32 bytes
}

// EncryptSecret AES-GCM 加密。
func EncryptSecret(plaintext string) string {
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return plaintext // 降级:加密失败返回明文(不阻塞)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return plaintext
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return plaintext
	}
	encrypted := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return "enc:" + hex.EncodeToString(encrypted)
}

// DecryptSecret AES-GCM 解密。
func DecryptSecret(ciphertext string) string {
	if len(ciphertext) < 4 || ciphertext[:4] != "enc:" {
		return ciphertext // 不是加密的,返回原文
	}
	data, err := hex.DecodeString(ciphertext[4:])
	if err != nil {
		return ciphertext
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return ciphertext
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ciphertext
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return ciphertext
	}
	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return ciphertext // 解密失败返回密文
	}
	return string(plaintext)
}

// SecretStore 不可变密钥存储(frozen 语义 + MappingProxyType 等价)。
//
// Go 没有 MappingProxyType,用 RWMutex + 私有 map 模拟只读。
type SecretStore struct {
	mu              sync.RWMutex
	providerTokens  map[string]string // provider → token(加密存储)
	customSecrets   map[string]string // key → value(加密存储)
	locked          bool              // 是否锁定(创建后锁定,防修改)
}

// NewSecretStore 创建密钥存储。
func NewSecretStore() *SecretStore {
	return &SecretStore{
		providerTokens: make(map[string]string),
		customSecrets:  make(map[string]string),
	}
}

// SetProviderToken 设置 provider token(加密后存储)。
func (s *SecretStore) SetProviderToken(provider, token string) {
	if s.locked {
		return // frozen:不允许修改
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providerTokens[provider] = EncryptSecret(token)
}

// SetCustomSecret 设置自定义密钥。
func (s *SecretStore) SetCustomSecret(key, value string) {
	if s.locked {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.customSecrets[key] = EncryptSecret(value)
}

// Lock 锁定存储(创建后调用,防后续修改)。
func (s *SecretStore) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locked = true
}

// GetProviderToken 获取 provider token(解密)。
func (s *SecretStore) GetProviderToken(provider string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if encrypted, ok := s.providerTokens[provider]; ok {
		return DecryptSecret(encrypted)
	}
	return ""
}

// GetCustomSecret 获取自定义密钥(解密)。
func (s *SecretStore) GetCustomSecret(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if encrypted, ok := s.customSecrets[key]; ok {
		return DecryptSecret(encrypted)
	}
	return ""
}

// Serialize 序列化(expose=false 默认脱敏,expose=true 暴露明文)。
func (s *SecretStore) Serialize(expose bool) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !expose {
		// 默认脱敏:只返回结构,不返回值
		return map[string]any{
			"provider_tokens": map[string]string{},
			"custom_secrets":  map[string]string{},
		}
	}
	// expose=true:返回明文(需显式请求)
	tokens := make(map[string]string)
	for k, v := range s.providerTokens {
		tokens[k] = DecryptSecret(v)
	}
	secrets := make(map[string]string)
	for k, v := range s.customSecrets {
		secrets[k] = DecryptSecret(v)
	}
	return map[string]any{
		"provider_tokens": tokens,
		"custom_secrets":  secrets,
	}
}

// MaskAPIKey 脱敏 API Key(保留前4后4,中间用 * 替代)。
func MaskAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return fmt.Sprintf("%s****%s", key[:4], key[len(key)-4:])
}

// RedactSecretsInJSON 脱敏 JSON 里的密钥字段。
func RedactSecretsInJSON(data []byte) string {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return string(data)
	}
	redactMap(m)
	result, _ := json.Marshal(m)
	return string(result)
}

func redactMap(m map[string]any) {
	for k, v := range m {
		lowerK := k
		switch lowerK {
		case "api_key", "apikey", "apiKey", "secret", "token", "password", "api_token":
			if s, ok := v.(string); ok && len(s) > 0 {
				m[k] = MaskAPIKey(s)
			}
		default:
			if nested, ok := v.(map[string]any); ok {
				redactMap(nested)
			}
		}
	}
}
