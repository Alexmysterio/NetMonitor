package ws

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

// Access is the authorisation scope of one browser WebSocket connection.
// Restricted clients only receive frames that identify an allowed map or
// device; otherwise a viewer could observe the whole network in real time.
type Access struct {
	Username        string
	Maps            map[string]bool
	Devices         map[string]bool
	RestrictMaps    bool
	RestrictDevices bool
}

type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
	rdb     *redis.Client
	origin  string
	fanout  bool
}

type fanoutMessage struct {
	Origin string          `json:"origin"`
	MapID  string          `json:"map_id"`
	Data   json.RawMessage `json:"data"`
}

type Client struct {
	conn          *websocket.Conn
	send          chan []byte
	hub           *Hub
	mu            sync.RWMutex
	subscriptions map[string]bool
	all           bool
	user          string
	access        Access
}

func NewHub() *Hub {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return &Hub{clients: make(map[*Client]struct{}), origin: hex.EncodeToString(buf)}
}

// EnableFanout replicates WebSocket broadcasts through Redis so clients can
// connect to any core instance without sticky sessions.
func (h *Hub) EnableFanout(ctx context.Context, rdb *redis.Client) {
	h.rdb = rdb
	h.fanout = true
	go func() {
		sub := rdb.Subscribe(ctx, "netmonitor:ws")
		defer sub.Close()
		for msg := range sub.Channel() {
			var env fanoutMessage
			if json.Unmarshal([]byte(msg.Payload), &env) != nil || env.Origin == h.origin {
				continue
			}
			h.broadcast(env.Data, env.MapID)
		}
	}()
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  2048,
	WriteBufferSize: 2048,
	CheckOrigin: func(r *http.Request) bool {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			return true // Non-browser clients (agents, CLI) do not send Origin.
		}
		parsed, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(parsed.Hostname(), hostname(r.Host))
	},
}

func hostname(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return strings.Trim(hostport, "[]")
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request, access Access) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	c := &Client{
		conn:          conn,
		send:          make(chan []byte, 128),
		hub:           h,
		subscriptions: map[string]bool{},
		all:           !access.RestrictMaps && !access.RestrictDevices,
		user:          access.Username,
		access:        access,
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()

	go c.writePump()
	c.push(map[string]any{"type": "CONNECTED", "payload": map[string]any{"user": access.Username}})
	c.readPump()
}

func (h *Hub) remove(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	close(c.send)
}

func (c *Client) readPump() {
	defer func() {
		c.conn.Close()
		c.hub.remove(c)
	}()
	c.conn.SetReadLimit(64 * 1024)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg struct {
			Action string `json:"action"`
			MapID  string `json:"map_id"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Action {
		case "SUBSCRIBE_MAP":
			if msg.MapID != "" && c.access.RestrictMaps && !c.access.Maps[msg.MapID] {
				c.push(map[string]any{"type": "SUBSCRIPTION_DENIED", "map_id": msg.MapID})
				continue
			}
			c.mu.Lock()
			if msg.MapID != "" {
				c.subscriptions[msg.MapID] = true
			}
			c.all = false
			c.mu.Unlock()
			c.push(map[string]any{"type": "SUBSCRIBED", "map_id": msg.MapID})
		case "UNSUBSCRIBE_MAP":
			c.mu.Lock()
			delete(c.subscriptions, msg.MapID)
			c.mu.Unlock()
		case "PING":
			c.push(map[string]any{"type": "PONG"})
		}
	}
}

func (c *Client) writePump() {
	for data := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

func (c *Client) push(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
	}
}

func (h *Hub) broadcast(payload []byte, mapID string) {
	scope := scopeFromPayload(payload, mapID)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.mu.RLock()
		subscribed := mapID == "" || c.all || (mapID != "" && c.subscriptions[mapID])
		allowed := c.canSee(scope)
		c.mu.RUnlock()
		if !subscribed || !allowed {
			continue
		}
		select {
		case c.send <- payload:
		default:
			// Slow consumer: drop this frame; the next canvas refresh catches up.
		}
	}
}

type broadcastScope struct {
	MapID    string
	DeviceID string
	SubmapID string
}

func scopeFromPayload(payload []byte, mapID string) broadcastScope {
	scope := broadcastScope{MapID: mapID}
	var envelope struct {
		MapID   string `json:"map_id"`
		Payload struct {
			MapID         string `json:"map_id"`
			DeviceID      string `json:"device_id"`
			SubmapID      string `json:"submap_id"`
			SourceIP      string `json:"source_ip"`
			RootCauseID   string `json:"root_cause_device_id"`
			RootCauseName string `json:"root_cause"`
		} `json:"payload"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return scope
	}
	if scope.MapID == "" {
		scope.MapID = envelope.MapID
	}
	if scope.MapID == "" {
		scope.MapID = envelope.Payload.MapID
	}
	scope.DeviceID = envelope.Payload.DeviceID
	scope.SubmapID = envelope.Payload.SubmapID
	return scope
}

func (c *Client) canSee(scope broadcastScope) bool {
	if !c.access.RestrictMaps && !c.access.RestrictDevices {
		return true
	}
	hasIdentity := scope.MapID != "" || scope.DeviceID != "" || scope.SubmapID != ""
	if !hasIdentity {
		return false
	}
	if c.access.RestrictMaps {
		mapID := scope.MapID
		if mapID == "" {
			mapID = scope.SubmapID
		}
		if mapID == "" || !c.access.Maps[mapID] {
			return false
		}
	}
	if c.access.RestrictDevices {
		if scope.DeviceID == "" || !c.access.Devices[scope.DeviceID] {
			return false
		}
	}
	return true
}

// DisconnectUser closes active sockets for a user after a permissions or
// account change. The client reconnects and refreshes its scope from the DB.
func (h *Hub) DisconnectUser(username string) {
	if username == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.user == username {
			_ = c.conn.Close()
		}
	}
}

func (h *Hub) Broadcast(v any) {
	h.broadcastAndPublish("", v)
}

func (h *Hub) BroadcastMap(mapID string, v any) {
	h.broadcastAndPublish(mapID, v)
}

func (h *Hub) broadcastAndPublish(mapID string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	if h.fanout && h.rdb != nil {
		env, _ := json.Marshal(fanoutMessage{Origin: h.origin, MapID: mapID, Data: data})
		_ = h.rdb.Publish(context.Background(), "netmonitor:ws", env).Err()
	}
	h.broadcast(data, mapID)
}

func (h *Hub) Online() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients) > 0
}
