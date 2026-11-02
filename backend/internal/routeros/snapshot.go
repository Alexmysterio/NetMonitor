package routeros

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Snapshot is the safe, read-only inventory collected before and after a
// deployment. It is stored with each job target for forensic comparison.
type Snapshot struct {
	Identity  string   `json:"identity"`
	Version   string   `json:"version"`
	Model     string   `json:"model"`
	Serial    string   `json:"serial"`
	Board     string   `json:"board"`
	Uptime    string   `json:"uptime"`
	Packages  []string `json:"packages"`
	Resources Record   `json:"resources,omitempty"`
	System    Record   `json:"system,omitempty"`
	RouterBO  Record   `json:"routerboard,omitempty"`
}

// CollectSnapshot reads identity, resource and package information. A missing
// board menu (common on CHR/VM) is not an error.
func (c *Client) CollectSnapshot() (Snapshot, error) {
	var s Snapshot
	resource, err := c.Print("/system/resource/print")
	if err != nil {
		return s, fmt.Errorf("system/resource: %w", err)
	}
	if len(resource) > 0 {
		s.Resources = resource[0]
		s.Version = resource[0]["version"]
		s.Uptime = resource[0]["uptime"]
		s.Board = resource[0]["board-name"]
		s.Model = firstNonEmpty(resource[0]["model"], resource[0]["board-name"])
		s.Serial = resource[0]["serial-number"]
	}
	identity, err := c.Print("/system/identity/print")
	if err == nil && len(identity) > 0 {
		s.System = identity[0]
		s.Identity = identity[0]["name"]
	}
	if board, err := c.Print("/system/routerboard/print"); err == nil && len(board) > 0 {
		s.RouterBO = board[0]
		if s.Model == "" {
			s.Model = firstNonEmpty(board[0]["model"], board[0]["current-firmware"])
		}
		if s.Serial == "" {
			s.Serial = board[0]["serial-number"]
		}
	}
	pkgs, err := c.Print("/system/package/print")
	if err == nil {
		for _, p := range pkgs {
			if name := firstNonEmpty(p["name"], p["package"]); name != "" {
				s.Packages = append(s.Packages, name)
			}
		}
	}
	return s, nil
}

// Test is a read-only connectivity check used by the UI before a deployment.
func (c *Client) Test() (Snapshot, error) {
	return c.CollectSnapshot()
}

// RunScript deploys a reviewed RouterOS script under a unique temporary name,
// runs it and removes it from the device even when the run fails. The script
// only reaches RouterOS through the API; no local shell is involved.
func (c *Client) RunScript(ctx context.Context, name, script string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(script) == "" {
		return "", fmt.Errorf("el script está vacío")
	}
	if strings.ContainsRune(script, 0) {
		return "", fmt.Errorf("el script contiene bytes NUL")
	}
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	safeName := sanitizeScriptName(name)
	if safeName == "" {
		safeName = "netmonitor-deploy"
	}
	scriptID := safeName
	cleanup := func() error {
		_, err := c.sendWith("/system/script/remove", 8*time.Second, "=.id="+scriptID)
		return err
	}

	done, err := c.sendWith("/system/script/add", 12*time.Second,
		"=name="+safeName, "=source="+script, "=policy=ftp,reboot,read,write,policy,test,password,sniff,sensitive,romon")
	if err != nil {
		// Older RouterOS versions reject unknown policy bits; retry with the
		// conservative default used by the menu.
		done, err = c.sendWith("/system/script/add", 12*time.Second, "=name="+safeName, "=source="+script)
		if err != nil {
			return "", fmt.Errorf("crear script temporal: %w", err)
		}
	}
	if id := firstNonEmpty(done["ret"], safeName); id != "" {
		scriptID = id
	}
	if err := ctx.Err(); err != nil {
		_ = cleanup()
		return "", err
	}
	_, err = c.sendWith("/system/script/run", timeout, "=.id="+scriptID)
	cleanupErr := cleanup()
	if err != nil {
		if cleanupErr != nil {
			return "", fmt.Errorf("ejecutar script: %w (además falló la limpieza: %v)", err, cleanupErr)
		}
		return "", fmt.Errorf("ejecutar script: %w", err)
	}
	if cleanupErr != nil {
		return "Script ejecutado, pero no se pudo eliminar el script temporal", fmt.Errorf("limpiar script temporal: %w", cleanupErr)
	}
	return "Script ejecutado y eliminado del dispositivo", nil
}

func (c *Client) sendWith(command string, timeout time.Duration, words ...string) (Record, error) {
	if c == nil || c.conn == nil {
		return nil, fmt.Errorf("cliente RouterOS cerrado")
	}
	if timeout <= 0 {
		timeout = c.opts.Timeout
	}
	_ = c.conn.SetDeadline(time.Now().Add(timeout))
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
		}
	}
}

func sanitizeScriptName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
