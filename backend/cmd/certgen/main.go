package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	out := flag.String("out", "deploy/certs", "output directory")
	hosts := flag.String("hosts", "netmonitor,localhost", "comma-separated DNS SANs")
	ips := flag.String("ips", "127.0.0.1", "comma-separated IP SANs")
	flag.Parse()
	if err := os.MkdirAll(*out, 0755); err != nil {
		log.Fatal(err)
	}
	caKey, caCert, caDER := makeCA()
	writeKey(filepath.Join(*out, "ca.key"), caKey)
	writeCert(filepath.Join(*out, "ca.crt"), caDER)

	serverKey, serverDER := makeLeaf(caCert, caKey, "netmonitor", false, split(*hosts), splitIP(*ips))
	writeKey(filepath.Join(*out, "server.key"), serverKey)
	writeCert(filepath.Join(*out, "server.crt"), serverDER)

	clientKey, clientDER := makeLeaf(caCert, caKey, "netmonitor-agent", true, nil, nil)
	writeKey(filepath.Join(*out, "client.key"), clientKey)
	writeCert(filepath.Join(*out, "client.crt"), clientDER)
	log.Printf("certificates written to %s", *out)
}

func makeCA() (*ecdsa.PrivateKey, *x509.Certificate, []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "NetMonitor Agent CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		log.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return key, cert, der
}

func makeLeaf(ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, client bool, dns []string, ips []net.IP) (*ecdsa.PrivateKey, []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(3, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	if !client {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		log.Fatal(err)
	}
	return key, der
}

func randomSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		log.Fatal(err)
	}
	return n
}

func writeKey(path string, key *ecdsa.PrivateKey) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		log.Fatal(err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, data, 0600); err != nil {
		log.Fatal(err)
	}
}

func writeCert(path string, der []byte) {
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Fatal(err)
	}
}

func split(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func splitIP(s string) []net.IP {
	var out []net.IP
	for _, v := range split(s) {
		if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}
