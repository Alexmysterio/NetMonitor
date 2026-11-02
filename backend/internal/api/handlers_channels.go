package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/agents"
	"github.com/netmonitor/netmonitor/internal/secure"
)

func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	var kind, cfgRaw, titleTmpl, bodyTmpl string
	err := s.pool.QueryRow(r.Context(), `
		SELECT channel_type, config::text, title_template, body_template
		FROM notification_channels WHERE id=$1
	`, chi.URLParam(r, "id")).Scan(&kind, &cfgRaw, &titleTmpl, &bodyTmpl)
	if err != nil {
		writeError(w, http.StatusNotFound, "canal no encontrado")
		return
	}
	cfg := map[string]any{}
	_ = json.Unmarshal([]byte(secure.UnwrapJSON(cfgRaw)), &cfg)
	title := "NetMonitor · prueba de canal"
	body := "Si recibes este mensaje, el canal está configurado correctamente."
	if titleTmpl != "" {
		title = strings.NewReplacer("{{Status}}", "TEST", "{{Device.Name}}", "NetMonitor", "{{Event.Message}}", body).Replace(titleTmpl)
	}
	ctx := r.Context()
	switch kind {
	case "telegram":
		token, _ := cfg["bot_token"].(string)
		if token == "" {
			token, _ = cfg["token"].(string)
		}
		chat, _ := cfg["chat_id"].(string)
		if chat == "" {
			chat, _ = cfg["chat"].(string)
		}
		token = strings.TrimSpace(token)
		chat = strings.TrimSpace(chat)
		if token == "" || chat == "" {
			writeError(w, http.StatusBadRequest, "bot_token y chat_id son obligatorios")
			return
		}
		err = postChannelJSON(ctx, "https://api.telegram.org/bot"+token+"/sendMessage", map[string]any{
			"chat_id": chat, "text": "<b>" + title + "</b>\n" + body, "parse_mode": "HTML",
		})
	case "webhook", "slack", "discord", "teams", "ntfy":
		url, _ := cfg["url"].(string)
		if url == "" {
			writeError(w, http.StatusBadRequest, "url es obligatoria")
			return
		}
		err = postChannelJSON(ctx, url, map[string]any{"text": title + ": " + body, "title": title, "body": body})
	case "pagerduty":
		key, _ := cfg["routing_key"].(string)
		if key == "" {
			writeError(w, http.StatusBadRequest, "routing_key es obligatoria")
			return
		}
		err = postChannelJSON(ctx, "https://events.pagerduty.com/v2/enqueue", map[string]any{
			"routing_key": key, "event_action": "trigger", "dedup_key": "netmonitor-test",
			"payload": map[string]any{"summary": title, "source": "NetMonitor", "severity": "info"},
		})
	case "opsgenie":
		key, _ := cfg["api_key"].(string)
		if key == "" {
			writeError(w, http.StatusBadRequest, "api_key es obligatoria")
			return
		}
		base := "https://api.opsgenie.com"
		if region, _ := cfg["region"].(string); strings.EqualFold(region, "eu") {
			base = "https://api.eu.opsgenie.com"
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v2/alerts",
			strings.NewReader(`{"message":"NetMonitor test","description":"Prueba de canal","alias":"netmonitor-test","source":"NetMonitor"}`))
		if reqErr != nil {
			err = reqErr
			break
		}
		req.Header.Set("Authorization", "GenieKey "+key)
		req.Header.Set("Content-Type", "application/json")
		resp, reqErr := http.DefaultClient.Do(req)
		if reqErr != nil {
			err = reqErr
		} else {
			resp.Body.Close()
		}
	case "email":
		err = sendTestEmail(cfg, title, body)
	case "syslog":
		host, _ := cfg["host"].(string)
		if host == "" {
			host = "127.0.0.1"
		}
		port := 514
		if p, ok := cfg["port"].(float64); ok {
			port = int(p)
		}
		conn, dialErr := net.DialTimeout("udp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
		if dialErr != nil {
			err = dialErr
		} else {
			defer conn.Close()
			_, err = conn.Write([]byte("<134>NetMonitor channel test"))
		}
	case "agent_action":
		agentID, _ := cfg["agent_id"].(string)
		action, _ := cfg["action"].(string)
		if strings.EqualFold(action, "ping") {
			action = "icmp"
		}
		target, _ := cfg["target"].(string)
		if agentID == "" || action == "" || target == "" {
			err = fmt.Errorf("agent_id, target y action son obligatorios")
			break
		}
		if s.agentMgr == nil || !s.agentMgr.Online(agentID) {
			err = fmt.Errorf("el agente está offline")
			break
		}
		jobConfig := map[string]any{}
		if raw, ok := cfg["config"].(map[string]any); ok {
			jobConfig = raw
		}
		_, err = s.agentMgr.Execute(ctx, agentID, agents.Job{
			ID: "channel-test", DeviceID: "channel-test", ProbeType: action,
			IP: target, TimeoutMS: 5000, Config: jobConfig,
		})
	case "web_audio":
		s.hub.Broadcast(map[string]any{
			"type": "NOC_ALERT_EVENT",
			"payload": map[string]any{
				"transition": "recovered", "severity": "ok", "device_id": "", "device_name": "NetMonitor",
				"message": "Prueba de audio y TTS correcta", "timestamp": time.Now().UTC(),
			},
		})
	default:
		err = fmt.Errorf("tipo de canal no soportado")
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Prueba enviada correctamente"})
}

func postChannelJSON(ctx context.Context, url string, payload any) error {
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("el canal respondió HTTP %d", resp.StatusCode)
	}
	return nil
}

func sendTestEmail(cfg map[string]any, subject, body string) error {
	host, _ := cfg["host"].(string)
	from, _ := cfg["from"].(string)
	to, _ := cfg["to"].(string)
	user, _ := cfg["username"].(string)
	pass, _ := cfg["password"].(string)
	port := 587
	if p, ok := cfg["port"].(float64); ok {
		port = int(p)
	}
	if host == "" || from == "" || to == "" {
		return fmt.Errorf("host, from y to son obligatorios")
	}
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", from, to, subject, body))
	return smtp.SendMail(fmt.Sprintf("%s:%d", host, port), smtp.PlainAuth("", user, pass, host), from, []string{to}, msg)
}
