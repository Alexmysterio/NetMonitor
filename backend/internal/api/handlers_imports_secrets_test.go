package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/netmonitor/netmonitor/internal/dude"
	"github.com/netmonitor/netmonitor/internal/secure"
)

func TestEncryptSanitizeDecryptDudeSecrets(t *testing.T) {
	secure.SetKey("test-key")
	preview := dude.Preview{
		Devices:      []dude.Device{{Name: "R1", Username: "admin", Password: "secret"}},
		SNMPProfiles: []dude.SNMPProfile{{Name: "default", Community: "public"}},
	}
	if err := encryptDudePreviewSecrets(&preview); err != nil {
		t.Fatal(err)
	}
	if preview.Devices[0].Password != "" || preview.Devices[0].PasswordEncrypted == "" || !preview.Devices[0].HasPassword {
		t.Fatalf("device secret was not encrypted: %+v", preview.Devices[0])
	}
	if preview.SNMPProfiles[0].Community != "" || preview.SNMPProfiles[0].CommunityEncrypted == "" || !preview.SNMPProfiles[0].HasCommunity {
		t.Fatalf("community was not encrypted: %+v", preview.SNMPProfiles[0])
	}
	payload, _ := json.Marshal(preview)
	if strings.Contains(string(payload), "secret") || strings.Contains(string(payload), `"public"`) {
		t.Fatalf("payload leaked plaintext: %s", payload)
	}
	sanitizeDudePreview(&preview)
	if preview.Devices[0].PasswordEncrypted != "" || preview.SNMPProfiles[0].CommunityEncrypted != "" {
		t.Fatalf("ciphertext was not sanitized: %+v", preview)
	}
	sanitized, _ := json.Marshal(preview)
	for _, forbidden := range []string{"enc:v1:", "password_encrypted", "community_encrypted", "secret", `"public"`} {
		if strings.Contains(string(sanitized), forbidden) {
			t.Fatalf("sanitized preview leaked %q: %s", forbidden, sanitized)
		}
	}
	var stored dude.Preview
	if err := json.Unmarshal(payload, &stored); err != nil {
		t.Fatal(err)
	}
	if warnings := decryptDudePreviewSecrets(&stored); len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if stored.Devices[0].Password != "secret" || !stored.Devices[0].HasPassword {
		t.Fatalf("device secret was not restored: %+v", stored.Devices[0])
	}
	if stored.SNMPProfiles[0].Community != "public" || !stored.SNMPProfiles[0].HasCommunity {
		t.Fatalf("community was not restored: %+v", stored.SNMPProfiles[0])
	}
	if stored.Devices[0].PasswordDecryptFailed || stored.SNMPProfiles[0].CommunityDecryptFailed {
		t.Fatalf("successful decryption left failure flags set: %+v", stored)
	}
}

func TestEncryptDudePreviewSecretsEmptyValues(t *testing.T) {
	secure.SetKey("test-key")
	preview := dude.Preview{
		Devices:      []dude.Device{{Name: "R1"}},
		SNMPProfiles: []dude.SNMPProfile{{Name: "default"}},
	}
	if err := encryptDudePreviewSecrets(&preview); err != nil {
		t.Fatal(err)
	}
	if preview.Devices[0].Password != "" || preview.Devices[0].PasswordEncrypted != "" || preview.Devices[0].HasPassword {
		t.Fatalf("empty password changed: %+v", preview.Devices[0])
	}
	if preview.SNMPProfiles[0].Community != "" || preview.SNMPProfiles[0].CommunityEncrypted != "" || preview.SNMPProfiles[0].HasCommunity {
		t.Fatalf("empty community changed: %+v", preview.SNMPProfiles[0])
	}
}

func TestDecryptDudePreviewSecretsFailure(t *testing.T) {
	secure.SetKey("test-key")
	preview := dude.Preview{
		Devices: []dude.Device{{
			Name:              "R1",
			PasswordEncrypted: "enc:v1:AAAA",
			HasPassword:       true,
		}},
		SNMPProfiles: []dude.SNMPProfile{{
			Name:               "default",
			CommunityEncrypted: "enc:v1:AAAA",
			HasCommunity:       true,
		}},
	}
	warnings := decryptDudePreviewSecrets(&preview)
	if len(warnings) != 2 {
		t.Fatalf("expected two warnings, got %v", warnings)
	}
	if preview.Devices[0].Password != "" || preview.Devices[0].PasswordEncrypted != "" || preview.Devices[0].HasPassword {
		t.Fatalf("device secret was not cleared: %+v", preview.Devices[0])
	}
	if !preview.Devices[0].PasswordDecryptFailed {
		t.Fatalf("device decryption failure was not flagged: %+v", preview.Devices[0])
	}
	if preview.SNMPProfiles[0].Community != "" || preview.SNMPProfiles[0].CommunityEncrypted != "" || preview.SNMPProfiles[0].HasCommunity {
		t.Fatalf("community was not cleared: %+v", preview.SNMPProfiles[0])
	}
	if !preview.SNMPProfiles[0].CommunityDecryptFailed {
		t.Fatalf("community decryption failure was not flagged: %+v", preview.SNMPProfiles[0])
	}
}

func TestDecryptDudePreviewSecretsClearsFailureFlagsOnSuccess(t *testing.T) {
	secure.SetKey("test-key")
	encryptedPassword, err := secure.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	encryptedCommunity, err := secure.Encrypt("public")
	if err != nil {
		t.Fatal(err)
	}
	preview := dude.Preview{
		Devices: []dude.Device{{
			Name:                  "R1",
			PasswordEncrypted:     encryptedPassword,
			PasswordDecryptFailed: true,
			HasPassword:           true,
		}},
		SNMPProfiles: []dude.SNMPProfile{{
			Name:                   "default",
			CommunityEncrypted:     encryptedCommunity,
			CommunityDecryptFailed: true,
			HasCommunity:           true,
		}},
	}
	if warnings := decryptDudePreviewSecrets(&preview); len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if preview.Devices[0].PasswordDecryptFailed || preview.SNMPProfiles[0].CommunityDecryptFailed {
		t.Fatalf("successful decryption did not clear failure flags: %+v", preview)
	}
	if preview.Devices[0].Password != "secret" || preview.SNMPProfiles[0].Community != "public" {
		t.Fatalf("secrets were not restored: %+v", preview)
	}
}
