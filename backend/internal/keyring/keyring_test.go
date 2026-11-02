package keyring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/netmonitor/netmonitor/internal/config"
)

func TestFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.json")
	cfg := config.Config{EncryptionKeyFile: path}
	if err := Persist(cfg, Key{Current: "new", Previous: "old"}); err != nil {
		t.Fatal(err)
	}
	k, err := Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if k.Current != "new" || k.Previous != "old" || k.Source != "file" {
		t.Fatalf("unexpected key: %+v", k)
	}
}

func TestLegacyPlainFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.txt")
	if err := os.WriteFile(path, []byte("plain-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	k, err := Resolve(config.Config{EncryptionKeyFile: path})
	if err != nil || k.Current != "plain-key" {
		t.Fatalf("resolve = %+v, %v", k, err)
	}
}
