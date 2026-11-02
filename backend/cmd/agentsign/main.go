package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	gen := flag.Bool("gen", false, "generate a new keypair")
	keyFile := flag.String("key", "agent-update.key", "Ed25519 private key file")
	bin := flag.String("bin", "", "agent binary to sign")
	version := flag.String("version", "", "release version")
	flag.Parse()
	if *gen {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*keyFile, []byte(base64.StdEncoding.EncodeToString(priv)), 0600); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("private_key=%s\npublic_key=%s\n", *keyFile, base64.StdEncoding.EncodeToString(pub))
		return
	}
	if *bin == "" {
		log.Fatal("-bin is required")
	}
	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		log.Fatal(err)
	}
	priv, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		log.Fatal("invalid private key")
	}
	data, err := os.ReadFile(*bin)
	if err != nil {
		log.Fatal(err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	sig := ed25519.Sign(ed25519.PrivateKey(priv), []byte(hash))
	out, _ := json.MarshalIndent(map[string]string{
		"version": *version, "sha256": hash, "signature": base64.StdEncoding.EncodeToString(sig),
	}, "", "  ")
	fmt.Println(string(out))
}
