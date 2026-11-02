package secure

import (
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
