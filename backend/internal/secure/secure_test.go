package secure

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	SetKey("unit-test-key")
	enc, err := Encrypt("s3cr3t")
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(enc) || enc == "s3cr3t" {
		t.Fatal("value was not encrypted")
	}
	plain, err := Decrypt(enc)
	if err != nil || plain != "s3cr3t" {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
}

func TestPreviousKeyGrace(t *testing.T) {
	SetKey("old-key")
	enc, err := Encrypt("legacy-secret")
	if err != nil {
		t.Fatal(err)
	}
	SetKeys("new-key", "old-key")
	plain, err := Decrypt(enc)
	if err != nil || plain != "legacy-secret" {
		t.Fatalf("previous key decrypt = %q, %v", plain, err)
	}
	SetKey("new-key")
	if _, err := Decrypt(enc); err == nil {
		t.Fatal("old ciphertext must fail after grace key is removed")
	}
}

func TestJSONWrapperAndLegacy(t *testing.T) {
	SetKey("unit-test-key")
	wrapped, err := WrapJSON(`{"bot_token":"abc"}`)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(wrapped), &obj); err != nil || obj["enc"] == "" {
		t.Fatalf("wrapper invalid: %s", wrapped)
	}
	if got := UnwrapJSON(wrapped); got != `{"bot_token":"abc"}` {
		t.Fatalf("unwrapped = %q", got)
	}
	if got := UnwrapJSON(`{"sound":true}`); got != `{"sound":true}` {
		t.Fatalf("legacy plaintext changed: %q", got)
	}
}

func TestFingerprintStableForSameKeyAndValue(t *testing.T) {
	SetKey("fingerprint-test-key")
	first, err := Fingerprint("admin\x00secret")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Fingerprint("admin\x00secret")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("fingerprint is not deterministic: %q != %q", first, second)
	}
	if len(first) != 8 {
		t.Fatalf("unexpected fingerprint length: %q", first)
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("fingerprint is not hexadecimal: %q", first)
	}
}

func TestFingerprintDiffersByValue(t *testing.T) {
	SetKey("fingerprint-test-key")
	first, err := Fingerprint("admin\x00secret")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Fingerprint("admin\x00other")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("different values produced the same fingerprint: %q", first)
	}
}

func TestFingerprintDiffersByKey(t *testing.T) {
	SetKey("fingerprint-key-one")
	first, err := Fingerprint("admin\x00secret")
	if err != nil {
		t.Fatal(err)
	}
	SetKey("fingerprint-key-two")
	second, err := Fingerprint("admin\x00secret")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("different keys produced the same fingerprint: %q", first)
	}
}

func TestFingerprintRequiresInitialisedKey(t *testing.T) {
	// Reset the key directly instead of using SetKey(""), which installs the
	// development fallback key.
	mu.Lock()
	savedKey, savedPrevKey := key, prevKey
	key = nil
	prevKey = nil
	mu.Unlock()
	defer func() {
		mu.Lock()
		key, prevKey = savedKey, savedPrevKey
		mu.Unlock()
	}()
	if _, err := Fingerprint("value"); err == nil {
		t.Fatal("expected an error when the encryption key is not initialised")
	}
}
