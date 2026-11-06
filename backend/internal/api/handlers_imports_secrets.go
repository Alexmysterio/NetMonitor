package api

import (
	"fmt"

	"github.com/netmonitor/netmonitor/internal/dude"
	"github.com/netmonitor/netmonitor/internal/secure"
)

func encryptDudePreviewSecrets(preview *dude.Preview) error {
	for i := range preview.Devices {
		d := &preview.Devices[i]
		if d.Password == "" {
			continue
		}
		enc, err := secure.Encrypt(d.Password)
		if err != nil {
			return fmt.Errorf("cifrar contraseña de %s: %w", d.Name, err)
		}
		d.PasswordEncrypted = enc
		d.HasPassword = true
		d.Password = ""
	}
	for i := range preview.SNMPProfiles {
		p := &preview.SNMPProfiles[i]
		if p.Community == "" {
			continue
		}
		enc, err := secure.Encrypt(p.Community)
		if err != nil {
			return fmt.Errorf("cifrar community de %s: %w", p.Name, err)
		}
		p.CommunityEncrypted = enc
		p.HasCommunity = true
		p.Community = ""
	}
	return nil
}

func sanitizeDudePreview(preview *dude.Preview) {
	for i := range preview.Devices {
		preview.Devices[i].Password = ""
		preview.Devices[i].PasswordEncrypted = ""
	}
	for i := range preview.SNMPProfiles {
		preview.SNMPProfiles[i].Community = ""
		preview.SNMPProfiles[i].CommunityEncrypted = ""
	}
}

func decryptDudePreviewSecrets(preview *dude.Preview) []string {
	var warnings []string
	for i := range preview.Devices {
		d := &preview.Devices[i]
		if d.PasswordEncrypted == "" {
			continue
		}
		plain, err := secure.Decrypt(d.PasswordEncrypted)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("No se pudo descifrar la credencial de %s.", d.Name))
			d.PasswordEncrypted = ""
			d.HasPassword = false
			continue
		}
		d.Password = plain
		d.HasPassword = plain != ""
		d.PasswordEncrypted = ""
	}
	for i := range preview.SNMPProfiles {
		p := &preview.SNMPProfiles[i]
		if p.CommunityEncrypted == "" {
			continue
		}
		plain, err := secure.Decrypt(p.CommunityEncrypted)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("No se pudo descifrar la community SNMP de %s.", p.Name))
			p.CommunityEncrypted = ""
			p.HasCommunity = false
			continue
		}
		p.Community = plain
		p.HasCommunity = plain != ""
		p.CommunityEncrypted = ""
	}
	return warnings
}
