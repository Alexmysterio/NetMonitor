// Package keyring resolves and persists the encryption key from ENCRYPTION_KEY,
// a local JSON file or Vault KV v2.
package keyring

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/netmonitor/netmonitor/internal/config"
)

type Key struct {
	Current  string `json:"key"`
	Previous string `json:"previous,omitempty"`
	Source   string `json:"-"`
}

func Resolve(cfg config.Config) (Key, error) {
	if cfg.VaultAddr != "" && cfg.VaultToken != "" {
		k, err := readVault(cfg)
		if err != nil {
			return Key{}, err
		}
		k.Source = "vault"
		return k, nil
	}
	if cfg.EncryptionKeyFile != "" {
		k, err := readFile(cfg.EncryptionKeyFile)
		if err != nil {
			return Key{}, err
		}
		k.Source = "file"
		return k, nil
	}
	if cfg.EncryptionKeyCmd != "" {
		out, err := exec.Command("sh", "-c", cfg.EncryptionKeyCmd).Output()
		if err != nil {
			return Key{}, fmt.Errorf("ENCRYPTION_KEY_CMD: %w", err)
		}
		key := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		if key == "" {
			return Key{}, fmt.Errorf("ENCRYPTION_KEY_CMD no devolvió ninguna clave")
		}
		return Key{Current: key, Source: "cmd"}, nil
	}
	return Key{Current: cfg.EncryptionKey, Source: "env"}, nil
}

// Persist stores the new current key plus the old one as previous. Both are
// kept so a crash between persistence and re-encryption remains recoverable.
func Persist(cfg config.Config, k Key) error {
	switch {
	case cfg.VaultAddr != "" && cfg.VaultToken != "":
		return writeVault(cfg, k)
	case cfg.EncryptionKeyFile != "":
		return writeFile(cfg.EncryptionKeyFile, k)
	default:
		return fmt.Errorf("la rotación programada requiere ENCRYPTION_KEY_FILE o Vault")
	}
}

func readFile(path string) (Key, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Key{}, err
	}
	var k Key
	if json.Unmarshal(raw, &k) == nil && k.Current != "" {
		return k, nil
	}
	return Key{Current: strings.TrimSpace(string(raw))}, nil
}

func writeFile(path string, k Key) error {
	data, _ := json.MarshalIndent(map[string]string{"key": k.Current, "previous": k.Previous}, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readVault(cfg config.Config) (Key, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(cfg.VaultAddr, "/")+"/v1/"+strings.TrimLeft(cfg.VaultPath, "/"), nil)
	if err != nil {
		return Key{}, err
	}
	req.Header.Set("X-Vault-Token", cfg.VaultToken)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return Key{}, fmt.Errorf("vault: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return Key{}, fmt.Errorf("vault: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Data struct {
				Key      string `json:"key"`
				Previous string `json:"previous"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Key{}, err
	}
	if body.Data.Data.Key == "" {
		return Key{}, fmt.Errorf("vault: no se encontró la clave en %s", cfg.VaultPath)
	}
	return Key{Current: body.Data.Data.Key, Previous: body.Data.Data.Previous}, nil
}

func writeVault(cfg config.Config, k Key) error {
	payload, _ := json.Marshal(map[string]any{"data": map[string]string{"key": k.Current, "previous": k.Previous}})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.VaultAddr, "/")+"/v1/"+strings.TrimLeft(cfg.VaultPath, "/"), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", cfg.VaultToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("vault: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("vault: HTTP %d", resp.StatusCode)
	}
	return nil
}
