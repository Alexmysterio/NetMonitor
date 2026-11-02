// Package routeros implements the small, auditable subset of the RouterOS API
// that NetMonitor needs to inventory devices and deploy reviewed scripts.
//
// It deliberately does not shell out and never accepts an arbitrary host from a
// script: every call is bound to a device already present in the inventory and
// to encrypted credentials stored in routeros_credentials.
package routeros

import (
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	maxWordLength = 1 << 20
	maxSentence   = 8 << 20
)

// Options describes a single RouterOS API connection.
type Options struct {
	Host        string
	Port        int
	Username    string
	Password    string
	TLS         bool
	InsecureTLS bool
	Timeout     time.Duration
}

// Client is a tiny RouterOS API v6.43+ client. It is not safe for concurrent
// use: jobs open one connection per target and close it immediately.
type Client struct {
	conn   net.Conn
	opts   Options
	closed bool
}

// Record is a `!re` response row. Keys keep RouterOS syntax, e.g. "name".
type Record map[string]string

// Connect opens the API socket and authenticates using the plain password flow
// (RouterOS >= 6.43) with fallback to the legacy challenge/response flow.
func Connect(ctx context.Context, opts Options) (*Client, error) {
	if strings.TrimSpace(opts.Host) == "" {
		return nil, fmt.Errorf("host is required")
	}
	if opts.Port <= 0 {
		if opts.TLS {
			opts.Port = 8729
		} else {
			opts.Port = 8728
		}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 8 * time.Second
	}
	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	dialer := &net.Dialer{Timeout: opts.Timeout}
	var conn net.Conn
	var err error
	if opts.TLS {
		td := &tls.Dialer{
			NetDialer: dialer,
			Config: &tls.Config{
				ServerName:         opts.Host,
				InsecureSkipVerify: opts.InsecureTLS, // Explicit per-credential setting, shown in the UI.
				MinVersion:         tls.VersionTLS12,
			},
		}
		conn, err = td.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("conectar a %s: %w", addr, err)
	}
	c := &Client{conn: conn, opts: opts}
	_ = conn.SetDeadline(time.Now().Add(opts.Timeout))
	if err := c.login(); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) Close() error {
	if c == nil || c.closed || c.conn == nil {
		return nil
	}
	c.closed = true
	return c.conn.Close()
}

func (c *Client) login() error {
	done, err := c.send("/login", "=name="+c.opts.Username, "=password="+c.opts.Password)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	challenge := done["ret"]
	if challenge == "" {
		return nil // Modern RouterOS accepted the password.
	}
	raw, err := hex.DecodeString(challenge)
	if err != nil {
		return fmt.Errorf("challenge inválido: %w", err)
	}
	h := md5.New()
	h.Write([]byte{0})
	h.Write([]byte(c.opts.Password))
	h.Write(raw)
	response := "00" + hex.EncodeToString(h.Sum(nil))
	if _, err := c.send("/login", "=name="+c.opts.Username, "=response="+response); err != nil {
		return fmt.Errorf("login legacy: %w", err)
	}
	return nil
}

// Send executes a command and returns the final `!done` attributes. It is
// exported for advanced callers; most code should use Print or RunScript.
func (c *Client) Send(command string, words ...string) (Record, error) {
	return c.send(command, words...)
}

// Print runs a `/print` command and returns every `!re` row. A non-fatal empty
// result is common on devices without that menu, so callers can decide.
func (c *Client) Print(command string, words ...string) ([]Record, error) {
	if c == nil || c.conn == nil {
		return nil, fmt.Errorf("cliente RouterOS cerrado")
	}
	_ = c.conn.SetDeadline(time.Now().Add(c.opts.Timeout))
	if err := c.writeSentence(append([]string{command}, words...)); err != nil {
		return nil, err
	}
	var rows []Record
	for {
		sentence, err := c.readSentence()
		if err != nil {
			return nil, err
		}
		if len(sentence) == 0 {
			continue
		}
		switch sentence[0] {
		case "!re":
			rows = append(rows, parseRecord(sentence[1:]))
		case "!done":
			return rows, nil
		case "!trap", "!fatal":
			return rows, fmt.Errorf("%s", trapMessage(sentence))
		default:
			// Ignore `!empty` and unknown control words.
		}
	}
}

func (c *Client) send(command string, words ...string) (Record, error) {
	if c == nil || c.conn == nil {
		return nil, fmt.Errorf("cliente RouterOS cerrado")
	}
	_ = c.conn.SetDeadline(time.Now().Add(c.opts.Timeout))
	if err := c.writeSentence(append([]string{command}, words...)); err != nil {
		return nil, err
	}
	for {
		sentence, err := c.readSentence()
		if err != nil {
			return nil, err
		}
		if len(sentence) == 0 {
			continue
		}
		switch sentence[0] {
		case "!done":
			return parseRecord(sentence[1:]), nil
		case "!trap", "!fatal":
			return nil, fmt.Errorf("%s", trapMessage(sentence))
		case "!re":
			continue
		default:
			continue
		}
	}
}

func trapMessage(sentence []string) string {
	record := parseRecord(sentence[1:])
	for _, key := range []string{"message", "category", "section"} {
		if v := strings.TrimSpace(record[key]); v != "" {
			return v
		}
	}
	return "RouterOS rechazó el comando"
}

func parseRecord(words []string) Record {
	out := Record{}
	for _, word := range words {
		if !strings.HasPrefix(word, "=") {
			continue
		}
		kv := strings.TrimPrefix(word, "=")
		key, value, found := strings.Cut(kv, "=")
		if found {
			out[key] = value
		} else {
			out[key] = ""
		}
	}
	return out
}

func (c *Client) writeSentence(words []string) error {
	for _, word := range words {
		if err := writeWord(c.conn, word); err != nil {
			return err
		}
	}
	return writeWord(c.conn, "")
}

func writeWord(w io.Writer, word string) error {
	n := len(word)
	switch {
	case n < 0x80:
		if _, err := w.Write([]byte{byte(n)}); err != nil {
			return err
		}
	case n < 0x4000:
		if _, err := w.Write([]byte{byte(n>>8) | 0x80, byte(n)}); err != nil {
			return err
		}
	case n < 0x200000:
		if _, err := w.Write([]byte{byte(n>>16) | 0xC0, byte(n >> 8), byte(n)}); err != nil {
			return err
		}
	case n < 0x10000000:
		if _, err := w.Write([]byte{byte(n>>24) | 0xE0, byte(n >> 16), byte(n >> 8), byte(n)}); err != nil {
			return err
		}
	default:
		if _, err := w.Write([]byte{0xF0, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, word)
	return err
}

func (c *Client) readSentence() ([]string, error) {
	var words []string
	total := 0
	for {
		word, err := readWord(c.conn)
		if err != nil {
			return nil, err
		}
		if word == "" {
			return words, nil
		}
		total += len(word)
		if total > maxSentence {
			return nil, fmt.Errorf("respuesta RouterOS demasiado grande")
		}
		words = append(words, word)
	}
}

func readWord(r io.Reader) (string, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return "", err
	}
	length := int(first[0])
	if first[0]&0x80 != 0 {
		if first[0]&0xC0 == 0x80 {
			var b [1]byte
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return "", err
			}
			length = (length&0x3F)<<8 | int(b[0])
		} else if first[0]&0xE0 == 0xC0 {
			var b [2]byte
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return "", err
			}
			length = (length&0x1F)<<16 | int(b[0])<<8 | int(b[1])
		} else if first[0]&0xF0 == 0xE0 {
			var b [3]byte
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return "", err
			}
			length = (length&0x0F)<<24 | int(b[0])<<16 | int(b[1])<<8 | int(b[2])
		} else {
			var b [4]byte
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return "", err
			}
			length = int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
		}
	}
	if length == 0 {
		return "", nil
	}
	if length < 0 || length > maxWordLength {
		return "", fmt.Errorf("longitud RouterOS inválida: %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
