package agents

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SNMPConfig struct {
	Version   string `json:"version"`
	Port      int    `json:"port"`
	Community string `json:"community"`
	User      string `json:"user"`
	Security  string `json:"security"`
	AuthProto string `json:"auth_proto"`
	AuthPass  string `json:"auth_pass"`
	PrivProto string `json:"priv_proto"`
	PrivPass  string `json:"priv_pass"`
}

type Job struct {
	ID            string         `json:"id"`
	DeviceID      string         `json:"device_id"`
	ProbeType     string         `json:"probe_type"`
	IP            string         `json:"ip"`
	TimeoutMS     int            `json:"timeout_ms"`
	Config        map[string]any `json:"config"`
	AvailableExpr string         `json:"available_expression"`
	WarningExpr   string         `json:"warning_expression"`
	ErrorExpr     string         `json:"error_expression"`
	ValueExpr     string         `json:"value_expression"`
	Unit          string         `json:"unit"`
	SNMP          SNMPConfig     `json:"snmp"`
}

type Result struct {
	OK       bool                 `json:"ok"`
	Warning  bool                 `json:"warning"`
	HasValue bool                 `json:"has_value"`
	Value    float64              `json:"value"`
	RTT      float64              `json:"rtt_ms"`
	Loss     float64              `json:"loss_pct"`
	Message  string               `json:"message"`
	OIDs     map[string]any       `json:"oids,omitempty"`
	Columns  map[string][]float64 `json:"columns,omitempty"`
	Data     map[string]any       `json:"data,omitempty"`
}

type Manager struct {
	pool    *pgxpool.Pool
	mu      sync.RWMutex
	clients map[string]*client
}

type client struct {
	id      string
	conn    *websocket.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan Result
}

func New(pool *pgxpool.Pool) *Manager {
	return &Manager{pool: pool, clients: map[string]*client{}}
}

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func RandomToken() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func (m *Manager) ServeWS(w http.ResponseWriter, r *http.Request, agentID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{id: agentID, conn: conn, pending: map[string]chan Result{}}
	m.mu.Lock()
	if old := m.clients[agentID]; old != nil {
		_ = old.conn.Close()
	}
	m.clients[agentID] = c
	m.mu.Unlock()
	_ = m.touch(agentID, "", true)
	m.send(c, map[string]any{"type": "WELCOME", "agent_id": agentID})
	defer func() {
		m.mu.Lock()
		if m.clients[agentID] == c {
			delete(m.clients, agentID)
		}
		m.mu.Unlock()
		c.failAll("agent disconnected")
		_ = conn.Close()
		_ = m.touch(agentID, "", false)
	}()
	conn.SetReadLimit(1 << 20)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Version string `json:"version"`
			Result  Result `json:"result"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "HELLO":
			_ = m.touch(agentID, msg.Version, true)
		case "RESULT":
			c.mu.Lock()
			ch := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- msg.Result:
				default:
				}
			}
		case "PING":
			m.send(c, map[string]any{"type": "PONG"})
		}
	}
}

func (m *Manager) Execute(ctx context.Context, agentID string, job Job) (Result, error) {
	m.mu.RLock()
	c := m.clients[agentID]
	m.mu.RUnlock()
	if c == nil {
		return Result{}, fmt.Errorf("agent %s is offline", agentID)
	}
	id := randomID()
	ch := make(chan Result, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	payload := map[string]any{"type": "JOB", "id": id, "job": job}
	if err := m.send(c, payload); err != nil {
		return Result{}, err
	}
	wait := time.Duration(job.TimeoutMS)*time.Millisecond + 5*time.Second
	if wait < 8*time.Second {
		wait = 8 * time.Second
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-timer.C:
		return Result{}, fmt.Errorf("agent timeout")
	case result := <-ch:
		if result.Message == "agent disconnected" {
			return Result{}, fmt.Errorf("agent disconnected")
		}
		return result, nil
	}
}

// Update sends a signed binary update instruction to a connected agent.
func (m *Manager) Update(agentID, version, url, sha256sum, signature string) error {
	m.mu.RLock()
	c := m.clients[agentID]
	m.mu.RUnlock()
	if c == nil {
		return fmt.Errorf("agent %s is offline", agentID)
	}
	return m.send(c, map[string]any{
		"type": "UPDATE",
		"update": map[string]string{
			"version": version, "url": url, "sha256": sha256sum, "signature": signature,
		},
	})
}

func (m *Manager) Online(agentID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clients[agentID] != nil
}

func (m *Manager) send(c *client, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

func (m *Manager) touch(agentID, version string, online bool) error {
	if version != "" {
		_, err := m.pool.Exec(context.Background(), `UPDATE agents SET last_seen_at=NOW(), version=$2 WHERE id=$1`, agentID, version)
		return err
	}
	if online {
		_, err := m.pool.Exec(context.Background(), `UPDATE agents SET last_seen_at=NOW() WHERE id=$1`, agentID)
		return err
	}
	_, err := m.pool.Exec(context.Background(), `UPDATE agents SET last_seen_at=NOW() WHERE id=$1`, agentID)
	return err
}

func (c *client) failAll(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		select {
		case ch <- Result{OK: false, Message: reason}:
		default:
		}
	}
}

func randomID() string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
