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
		Devices:      []dude.Device{{Name: "R1", Username: "admin", Password: "secret", HasPassword: true}},
		SNMPProfiles: []dude.SNMPProfile{{Name: "default", Community: "public", HasCommunity: true}},
	}
	if err := encryptDudePreviewSecrets(&preview); err != nil {
		t.Fatal(err)
	}
	if preview.Devices[0].Password != "" || preview.Devices[0].PasswordEncrypted == "" {
		t.Fatalf("device secret was not encrypted: %+v", preview.Devices[0])
	}
	if preview.SNMPProfiles[0].Community != "" || preview.SNMPProfiles[0].CommunityEncrypted == "" {
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
	var stored dude.Preview
	if err := json.Unmarshal(payload, &stored); err != nil {
		t.Fatal(err)
	}
	if warnings := decryptDudePreviewSecrets(&stored); len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if stored.Devices[0].Password != "secret" || stored.SNMPProfiles[0].Community != "public" {
		t.Fatalf("secrets were not restored: %+v", stored)
	}
}
