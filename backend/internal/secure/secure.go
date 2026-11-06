package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// The codec keeps SNMP credentials and notification-channel secrets encrypted
// at rest. The key is supplied by ENCRYPTION_KEY (or derived from JWT_SECRET in
// development) and must remain stable across restarts.
var (
	mu      sync.RWMutex
	key     []byte
	prevKey []byte
)

const prefix = "enc:v1:"

func SetKey(raw string) {
	SetKeys(raw, "")
}

// SetKeys installs the current key and an optional previous key. Decrypt tries
// both, which makes key rotation and rollback safe while old secrets are still
// being re-encrypted.
func SetKeys(raw, previous string) {
	if raw == "" {
		raw = "development-only-encryption-key"
	}
	sum := sha256.Sum256([]byte(raw))
	mu.Lock()
	key = sum[:]
	if previous != "" {
		prev := sha256.Sum256([]byte(previous))
		prevKey = prev[:]
	} else {
		prevKey = nil
	}
	mu.Unlock()
}

func currentKey() []byte {
	mu.RLock()
	defer mu.RUnlock()
	return key
}

func previousKey() []byte {
	mu.RLock()
	defer mu.RUnlock()
	return prevKey
}

func IsEncrypted(v string) bool { return strings.HasPrefix(v, prefix) }

// Fingerprint returns a short deterministic keyed digest for identifiers that
// must be stable across imports but must not reveal the original secret.
func Fingerprint(value string) (string, error) {
	k := currentKey()
	if len(k) != 32 {
		return "", fmt.Errorf("encryption key is not initialised")
	}
	mac := hmac.New(sha256.New, k)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))[:8], nil
}

func Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	k := currentKey()
	if len(k) != 32 {
		return "", fmt.Errorf("encryption key is not initialised")
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

// Decrypt accepts both encrypted and legacy plaintext values, which makes
// upgrades from the first versions of NetMonitor safe.
func Decrypt(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	if !IsEncrypted(v) {
		return v, nil
	}
	k := currentKey()
	if len(k) != 32 {
		return "", fmt.Errorf("encryption key is not initialised")
	}
	plain, err := decryptWith(v, k)
	if err == nil {
		return plain, nil
	}
	if pk := previousKey(); len(pk) == 32 {
		return decryptWith(v, pk)
	}
	return "", err
}

func decryptWith(v string, k []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, prefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("encrypted payload is truncated")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// WrapJSON encrypts an arbitrary JSON document into a JSONB-safe wrapper:
// {"enc":"enc:v1:..."}.
func WrapJSON(raw string) (string, error) {
	enc, err := Encrypt(raw)
	if err != nil {
		return "", err
	}
	payload, _ := json.Marshal(map[string]string{"enc": enc})
	return string(payload), nil
}

// UnwrapJSON understands both {"enc":"..."} wrappers and legacy plain objects.
func UnwrapJSON(raw string) string {
	var wrapper struct {
		Enc string `json:"enc"`
	}
	if json.Unmarshal([]byte(raw), &wrapper) == nil && wrapper.Enc != "" {
		if plain, err := Decrypt(wrapper.Enc); err == nil {
			return plain
		}
	}
	return raw
}

func IsWrappedJSON(raw string) bool {
	var wrapper struct {
		Enc string `json:"enc"`
	}
	return json.Unmarshal([]byte(raw), &wrapper) == nil && wrapper.Enc != ""
}
