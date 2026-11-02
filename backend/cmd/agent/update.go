package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type updateSpec struct {
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

func applyUpdate(u updateSpec) error {
	pubRaw := os.Getenv("AGENT_UPDATE_PUBLIC_KEY")
	if pubRaw == "" {
		return fmt.Errorf("AGENT_UPDATE_PUBLIC_KEY no está configurada")
	}
	pub, err := base64.StdEncoding.DecodeString(pubRaw)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("AGENT_UPDATE_PUBLIC_KEY inválida")
	}
	sig, err := base64.StdEncoding.DecodeString(u.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("firma inválida")
	}
	expected := strings.ToLower(strings.TrimSpace(u.SHA256))
	if len(expected) != 64 {
		return fmt.Errorf("sha256 inválido")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(expected), sig) {
		return fmt.Errorf("la firma no coincide con el hash")
	}

	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Get(u.URL)
	if err != nil {
		return fmt.Errorf("descarga: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("descarga: HTTP %d", resp.StatusCode)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".netmonitor-agent-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(resp.Body, 128<<20)); err != nil {
		tmp.Close()
		return fmt.Errorf("escritura: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != expected {
		return fmt.Errorf("hash descargado %s no coincide", actual)
	}
	if err := os.Chmod(tmpName, 0755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, exe); err != nil {
		return fmt.Errorf("reemplazo: %w", err)
	}
	return restartSelf(exe)
}
