package routeros

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeRouterOS speaks the minimum RouterOS API needed by the inventory and
// script flows. It is intentionally protocol-level so the client wire format
// is verified, not mocked away.
func fakeRouterOS(t *testing.T, scriptSource *string) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	host, portRaw, _ := net.SplitHostPort(ln.Addr().String())
	port, _ = strconv.Atoi(portRaw)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		reader := &fakeConn{conn: conn}
		for {
			sentence, err := reader.readSentence()
			if err != nil || len(sentence) == 0 {
				return
			}
			switch sentence[0] {
			case "/login":
				_ = reader.writeSentence([]string{"!done"})
			case "/system/resource/print":
				_ = reader.writeSentence([]string{"!re", "=version=7.15.3", "=board-name=RB5009", "=model=RB5009UG+S+", "=serial-number=ABC123", "=uptime=1w2d"})
				_ = reader.writeSentence([]string{"!done"})
			case "/system/identity/print":
				_ = reader.writeSentence([]string{"!re", "=name=core-test"})
				_ = reader.writeSentence([]string{"!done"})
			case "/system/routerboard/print":
				_ = reader.writeSentence([]string{"!re", "=model=RB5009UG+S+", "=serial-number=ABC123"})
				_ = reader.writeSentence([]string{"!done"})
			case "/system/package/print":
				_ = reader.writeSentence([]string{"!re", "=name=routeros", "=version=7.15.3"})
				_ = reader.writeSentence([]string{"!done"})
			case "/system/script/add":
				for _, word := range sentence[1:] {
					if strings.HasPrefix(word, "=source=") && scriptSource != nil {
						*scriptSource = strings.TrimPrefix(word, "=source=")
					}
				}
				_ = reader.writeSentence([]string{"!done", "=ret=*1"})
			case "/system/script/run", "/system/script/remove":
				_ = reader.writeSentence([]string{"!done"})
			default:
				_ = reader.writeSentence([]string{"!done"})
			}
		}
	}()
	return host, port
}

type fakeConn struct {
	conn net.Conn
}

func (f *fakeConn) readSentence() ([]string, error) {
	var words []string
	for {
		word, err := readWord(f.conn)
		if err != nil {
			return nil, err
		}
		if word == "" {
			return words, nil
		}
		words = append(words, word)
	}
}

func (f *fakeConn) writeSentence(words []string) error {
	for _, word := range words {
		if err := writeWord(f.conn, word); err != nil {
			return err
		}
	}
	return writeWord(f.conn, "")
}

func TestCollectSnapshotAndRunScript(t *testing.T) {
	var source string
	host, port := fakeRouterOS(t, &source)
	client, err := Connect(context.Background(), Options{
		Host: host, Port: port, Username: "netmonitor", Password: "secret", Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	snap, err := client.CollectSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != "7.15.3" || snap.Model != "RB5009UG+S+" || snap.Identity != "core-test" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if len(snap.Packages) != 1 || snap.Packages[0] != "routeros" {
		t.Fatalf("unexpected packages: %+v", snap.Packages)
	}
	output, err := client.RunScript(context.Background(), "deploy test", "/system identity set name=test\n", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if output == "" || source == "" || !strings.Contains(source, "identity set") {
		t.Fatalf("script not sent or output empty: output=%q source=%q", output, source)
	}
}

func TestWriteReadWordRoundTrip(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	values := []string{"", "short", strings.Repeat("x", 200), strings.Repeat("y", 5000)}
	go func() {
		for _, value := range values {
			_ = writeWord(left, value)
		}
		_ = writeWord(left, "")
	}()
	for _, want := range values {
		got, err := readWord(right)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("word mismatch: got %d bytes want %d", len(got), len(want))
		}
	}
	end, _ := readWord(right)
	if end != "" {
		t.Fatalf("expected sentence terminator, got %q", end)
	}
}
