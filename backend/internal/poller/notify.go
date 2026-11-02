package poller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/netmonitor/netmonitor/internal/agents"
	"github.com/netmonitor/netmonitor/internal/secure"
)

func jsonUnmarshal(raw string, out any) error { return json.Unmarshal([]byte(raw), out) }

type alertChannel struct {
	id, kind, cfg, title, body string
}

type alertRule struct {
	id             string
	channelIDs     []string
	delaySec       int
	repeatInterval int
	maxRepeats     int
	schedule       string
	groupBy        string
	groupWindow    int
}

// alertLimiter caps external notification sends per minute across all rules.
// Browser audio/TTS alerts are not limited because they stay inside the NOC.
type alertLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Time
	count  int
}

func (l *alertLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit <= 0 {
		return true
	}
	if l.window.IsZero() || now.Sub(l.window) >= time.Minute {
		l.window = now
		l.count = 0
	}
	if l.count >= l.limit {
		return false
	}
	l.count++
	return true
}

// notifyExternal applies alert rules asynchronously: delay, first send,
// acknowledgement check and periodic repeats. Several rules with different
// delays can be used to build escalation chains without blocking the poller.
func (p *Poller) alertTarget(ctx context.Context, deviceID string) (name, ip, mapID, mapName string) {
	_ = p.pool.QueryRow(ctx, `
		SELECT d.name, host(d.primary_ip), COALESCE(me.map_id::text,''), COALESCE(m.name,'')
		FROM devices d
		LEFT JOIN LATERAL (SELECT map_id FROM map_elements WHERE device_id=d.id LIMIT 1) me ON TRUE
		LEFT JOIN maps m ON m.id=me.map_id
		WHERE d.id=$1
	`, deviceID).Scan(&name, &ip, &mapID, &mapName)
	return
}

// NotifyAck sends the acknowledged transition to rules that subscribe to it.
func (p *Poller) NotifyAck(ctx context.Context, deviceID string) {
	if p.deviceBlocked(ctx, deviceID) {
		return
	}
	if ctx == nil {
		ctx = p.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	name, ip, mapID, mapName := p.alertTarget(ctx, deviceID)
	p.notifyExternal(ctx, deviceID, mapID, name, ip, mapName, "acknowledged", "Incidencia reconocida")
}

// NotifyTrap lets the trap receiver reuse the same alert rules and channels as
// polling transitions. Trap rules decide whether a trap reaches this method.
func (p *Poller) NotifyTrap(ctx context.Context, deviceID, reason string) {
	if deviceID == "" || p.deviceBlocked(ctx, deviceID) {
		return
	}
	name, ip, mapID, mapName := p.alertTarget(ctx, deviceID)
	p.notifyExternal(ctx, deviceID, mapID, name, ip, mapName, "snmp_trap", reason)
}

// NotifyTrapChannels sends a trap to an explicit channel list. It is used when
// a trap rule selects its own recipients, bypassing down/recovered filters.
func (p *Poller) NotifyTrapChannels(ctx context.Context, deviceID, reason string, channelIDs []string) {
	if deviceID == "" || len(channelIDs) == 0 || p.deviceBlocked(ctx, deviceID) {
		return
	}
	if !p.alertStillRelevant(ctx, deviceID, "snmp_trap") {
		return
	}
	name, ip, mapID, mapName := p.alertTarget(ctx, deviceID)
	channels := p.loadAlertChannels(ctx, channelIDs)
	if len(channels) > 0 {
		p.sendChannels(ctx, channels, deviceID, mapID, name, ip, mapName, "snmp_trap", reason)
	}
}

func (p *Poller) notifyFlap(ctx context.Context, deviceID, reason string) {
	if p.deviceBlocked(ctx, deviceID) {
		return
	}
	name, ip, mapID, mapName := p.alertTarget(ctx, deviceID)
	p.notifyExternal(ctx, deviceID, mapID, name, ip, mapName, "flapping", reason)
}

func (p *Poller) deviceBlocked(ctx context.Context, deviceID string) bool {
	var blocked bool
	_ = p.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM address_lists l
			JOIN address_list_entries e ON e.list_id=l.id
			JOIN devices d ON d.id=$1
			WHERE l.kind='block' AND d.primary_ip << e.value
		)
	`, deviceID).Scan(&blocked)
	return blocked
}

func (p *Poller) notifyExternal(ctx context.Context, deviceID, mapID, device, ip, mapName, transition, reason string) {
	if p.deviceBlocked(ctx, deviceID) {
		return
	}
	var tags []string
	_ = p.pool.QueryRow(ctx, `SELECT COALESCE(tags,'{}') FROM devices WHERE id=$1`, deviceID).Scan(&tags)
	ruleRows, err := p.pool.Query(ctx, `
		SELECT id, channel_ids, delay_sec, repeat_interval_sec, max_repeats, active_schedule::text,
		       group_by, group_window_sec
		FROM alert_rules
		WHERE is_enabled=TRUE
		  AND $1::text = ANY(trigger_transitions)
		  AND (cardinality(filter_device_ids)=0 OR $2::uuid = ANY(filter_device_ids))
		  AND (cardinality(filter_tags)=0 OR filter_tags && $3::text[])
		  AND (cardinality(filter_map_ids)=0 OR $4::uuid = ANY(filter_map_ids))
		  AND (cardinality(filter_address_list_ids)=0 OR EXISTS(
		      SELECT 1 FROM address_list_entries e JOIN devices dd ON dd.id=$2::uuid
		      WHERE e.list_id = ANY(filter_address_list_ids) AND dd.primary_ip << e.value))
		ORDER BY priority
	`, transition, nullableUUID(deviceID), tags, nullableUUID(mapID))
	if err != nil {
		log.Printf("alert rules: %v", err)
		return
	}
	var rules []alertRule
	for ruleRows.Next() {
		var r alertRule
		if ruleRows.Scan(&r.id, &r.channelIDs, &r.delaySec, &r.repeatInterval, &r.maxRepeats, &r.schedule, &r.groupBy, &r.groupWindow) == nil && len(r.channelIDs) > 0 {
			if !p.scheduleAllows(r.schedule, time.Now()) {
				continue
			}
			rules = append(rules, r)
		}
	}
	ruleRows.Close()
	for _, r := range rules {
		rule := r
		switch rule.groupBy {
		case "map":
			scope, scopeName := mapID, mapName
			if scope == "" {
				scope, scopeName = "device:"+deviceID, "Sin mapa"
			}
			if scopeName == "" {
				scopeName = "Sin mapa"
			}
			p.enqueueGrouped(ctx, rule, "map:"+scope, scopeName, deviceID, device, ip, transition, reason)
		case "tag":
			tag := "sin-etiqueta"
			if len(tags) > 0 {
				tag = tags[0]
			}
			p.enqueueGrouped(ctx, rule, "tag:"+strings.ToLower(tag), "Etiqueta: "+tag, deviceID, device, ip, transition, reason)
		case "submap":
			scope, scopeName := mapID, mapName
			if mapID != "" {
				var parentID, parentName string
				_ = p.pool.QueryRow(ctx, `
					SELECT COALESCE(m.parent_map_id::text,''), COALESCE(p.name,'')
					FROM maps m LEFT JOIN maps p ON p.id=m.parent_map_id
					WHERE m.id=$1
				`, mapID).Scan(&parentID, &parentName)
				if parentID != "" {
					scope, scopeName = parentID, parentName
				}
			}
			if scope == "" {
				scope, scopeName = "device:"+deviceID, "Sin mapa"
			}
			p.enqueueGrouped(ctx, rule, "submap:"+scope, scopeName, deviceID, device, ip, transition, reason)
		case "zone":
			scope, scopeName := mapID, mapName
			if mapID != "" {
				var zid, zname string
				_ = p.pool.QueryRow(ctx, `
					SELECT z.id::text, COALESCE(NULLIF(z.label_template,''),'Zona')
					FROM map_elements d
					JOIN map_elements z ON z.map_id=d.map_id AND z.element_type='zone'
					WHERE d.map_id=$1 AND d.device_id=$2
					  AND d.pos_x >= z.pos_x AND d.pos_x <= z.pos_x + z.width
					  AND d.pos_y >= z.pos_y AND d.pos_y <= z.pos_y + z.height
					ORDER BY z.z_index DESC LIMIT 1
				`, mapID, deviceID).Scan(&zid, &zname)
				if zid != "" {
					scope, scopeName = zid, zname
				}
			}
			if scope == "" {
				scope, scopeName = "device:"+deviceID, "Sin zona"
			}
			p.enqueueGrouped(ctx, rule, "zone:"+scope, scopeName, deviceID, device, ip, transition, reason)
		default:
			go p.runAlertRule(ctx, rule, deviceID, mapID, device, ip, mapName, transition, reason)
		}
	}
}

func (p *Poller) runAlertRule(ctx context.Context, rule alertRule, deviceID, mapID, device, ip, mapName, transition, reason string) {
	if rule.delaySec > 0 {
		timer := time.NewTimer(time.Duration(rule.delaySec) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	if !p.scheduleAllows(rule.schedule, time.Now()) {
		return
	}
	if !p.alertStillRelevant(ctx, deviceID, transition) {
		return
	}
	channels := p.loadAlertChannels(ctx, rule.channelIDs)
	if len(channels) == 0 {
		return
	}
	p.sendChannels(ctx, channels, deviceID, mapID, device, ip, mapName, transition, reason)
	if transition == "recovered" || transition == "acknowledged" || rule.repeatInterval <= 0 || rule.maxRepeats <= 0 {
		return
	}
	for i := 0; i < rule.maxRepeats; i++ {
		timer := time.NewTimer(time.Duration(rule.repeatInterval) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if !p.scheduleAllows(rule.schedule, time.Now()) {
			return
		}
		if !p.alertStillRelevant(ctx, deviceID, transition) {
			return
		}
		p.sendChannels(ctx, channels, deviceID, mapID, device, ip, mapName, transition, reason)
	}
}

func (p *Poller) alertStillRelevant(ctx context.Context, deviceID, transition string) bool {
	var status string
	var ack bool
	var ackExpires, maintenance *time.Time
	err := p.pool.QueryRow(ctx, `
		SELECT status, is_acknowledged, ack_expires_at, maintenance_until FROM devices WHERE id=$1
	`, deviceID).Scan(&status, &ack, &ackExpires, &maintenance)
	if err != nil {
		return false
	}
	if ack && ackExpires != nil && !ackExpires.After(time.Now()) {
		ack = false
	}
	if maintenance != nil && time.Now().Before(*maintenance) {
		return false
	}
	var scheduledMaintenance bool
	_ = p.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM maintenance_windows w
			WHERE w.starts_at <= NOW() AND w.ends_at > NOW()
			  AND (
			    (w.scope_type='device' AND w.scope_id=$1::uuid)
			    OR (w.scope_type='map' AND EXISTS(
			        SELECT 1 FROM map_elements me WHERE me.device_id=$1::uuid AND me.map_id=w.scope_id))
			    OR (w.scope_type='tag' AND EXISTS(
			        SELECT 1 FROM devices d WHERE d.id=$1::uuid AND w.scope_tag = ANY(d.tags)))
			    OR (w.scope_type='address_list' AND EXISTS(
			        SELECT 1 FROM address_list_entries e JOIN devices d ON d.id=$1::uuid
			        WHERE e.list_id=w.scope_id AND d.primary_ip << e.value))
			  )
		)
	`, deviceID).Scan(&scheduledMaintenance)
	if scheduledMaintenance {
		return false
	}
	if ack && transition != "recovered" && transition != "acknowledged" {
		return false
	}
	switch transition {
	case "down":
		return status == "down" || status == "unreachable"
	case "warning":
		return status == "warning"
	case "recovered":
		return status == "up"
	case "acknowledged":
		return ack
	case "flapping":
		return true
	case "snmp_trap":
		return true
	default:
		return false
	}
}

func (p *Poller) loadAlertChannels(ctx context.Context, ids []string) []alertChannel {
	rows, err := p.pool.Query(ctx, `
		SELECT id, channel_type, config::text, title_template, body_template
		FROM notification_channels WHERE is_enabled=TRUE AND id = ANY($1)
	`, ids)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []alertChannel
	for rows.Next() {
		var c alertChannel
		if rows.Scan(&c.id, &c.kind, &c.cfg, &c.title, &c.body) == nil {
			out = append(out, c)
		}
	}
	return out
}

func (p *Poller) sendChannels(ctx context.Context, channels []alertChannel, deviceID, mapID, device, ip, mapName, transition, reason string) {
	status := strings.ToUpper(transition)
	render := func(t string) string {
		r := strings.NewReplacer(
			"{{Status}}", status,
			"{{StatusUpper}}", status,
			"{{Device.Name}}", device,
			"{{Device.FirstAddress}}", ip,
			"{{Map.Name}}", mapName,
			"{{Event.Message}}", reason,
			"{{Event.Timestamp}}", time.Now().Format("2006-01-02 15:04:05"),
			"{{StatusIcon}}", statusIcon(transition),
		)
		return r.Replace(t)
	}
	now := time.Now()
	suppressed := false
	for _, c := range channels {
		if c.kind != "web_audio" && !p.alerts.allow(now) {
			if !suppressed {
				log.Printf("anti-storm: avisos externos suprimidos (límite %d/min)", p.alerts.limit)
				suppressed = true
			}
			continue
		}
		cfg := map[string]any{}
		_ = json.Unmarshal([]byte(secure.UnwrapJSON(c.cfg)), &cfg)
		title := render(c.title)
		body := render(c.body)
		switch c.kind {
		case "telegram":
			token, _ := cfg["bot_token"].(string)
			chat, _ := cfg["chat_id"].(string)
			if token == "" || chat == "" {
				continue
			}
			text := fmt.Sprintf("<b>%s</b>\n%s\n<a href=\"%s\">Abrir mapa</a>", title, body, p.cfg.PublicURL)
			payload := map[string]any{"chat_id": chat, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true}
			if deviceID != "" && (transition == "down" || transition == "warning" || transition == "flapping") {
				keyboard := [][]map[string]string{
					{{"text": "✅ Reconocer", "callback_data": "ack:" + deviceID}, {"text": "🔇 Silenciar 1h", "callback_data": "mute:" + deviceID}},
					{{"text": "🏓 Ping", "callback_data": "ping:" + deviceID}},
				}
				if mapID != "" {
					keyboard = append(keyboard, []map[string]string{{"text": "📍 Localizar en mapa", "url": strings.TrimRight(p.cfg.PublicURL, "/") + "/maps/" + mapID}})
				}
				payload["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
			}
			_ = postJSON(ctx, "https://api.telegram.org/bot"+token+"/sendMessage", payload)
		case "webhook", "slack", "discord", "teams", "ntfy":
			url, _ := cfg["url"].(string)
			if url == "" {
				continue
			}
			payload := map[string]any{
				"text":  fmt.Sprintf("%s: %s", title, body),
				"title": title, "body": body, "device": device, "ip": ip,
				"status": transition, "reason": reason, "map": mapName,
			}
			_ = postJSON(ctx, url, payload)
		case "pagerduty":
			_ = sendPagerDuty(ctx, cfg, title, body, transition, deviceID)
		case "opsgenie":
			_ = sendOpsgenie(ctx, cfg, title, body, transition, deviceID)
		case "sms":
			_ = sendSMS(ctx, cfg, title+"\n"+body)
		case "email":
			_ = sendEmail(cfg, title, body)
		case "agent_action", "runbook":
			p.runAgentAction(ctx, cfg, title, body, deviceID, ip, transition, reason)
		case "web_audio":
			// already broadcast over WebSocket
		}
	}
}

func (p *Poller) runAgentAction(ctx context.Context, cfg map[string]any, title, body, deviceID, ip, transition, reason string) {
	agentID, _ := cfg["agent_id"].(string)
	action, _ := cfg["action"].(string)
	if strings.EqualFold(action, "ping") {
		action = "icmp"
	}
	target, _ := cfg["target"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		target = ip
	}
	status := 200
	var message string
	if agentID == "" || p.agentMgr == nil || !p.agentMgr.Online(agentID) {
		status = 503
		message = "agente offline o no configurado"
	} else {
		allowed := map[string]bool{"icmp": true, "tcp": true, "http": true, "dns": true, "udp": true, "ssh": true, "smtp": true, "ftp": true}
		if !allowed[action] {
			status = 400
			message = "acción no permitida"
		} else {
			config := map[string]any{}
			if raw, ok := cfg["config"].(map[string]any); ok {
				for k, v := range raw {
					config[k] = v
				}
			}
			for _, key := range []string{"port", "send", "path", "scheme", "url", "expect_code", "expect_regex"} {
				if v, ok := cfg[key]; ok {
					config[key] = v
				}
			}
			job := agents.Job{
				ID: "action-" + deviceID, DeviceID: deviceID, ProbeType: action,
				IP: target, TimeoutMS: 5000, Config: config,
			}
			actionCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			res, err := p.agentMgr.Execute(actionCtx, agentID, job)
			if err != nil {
				status = 502
				message = err.Error()
			} else {
				message = res.Message
				if !res.OK {
					status = 502
				}
			}
		}
	}
	details, _ := json.Marshal(map[string]any{
		"action": action, "target": target, "device_id": deviceID,
		"transition": transition, "reason": reason, "message": message,
	})
	_, _ = p.pool.Exec(context.Background(), `
		INSERT INTO audit_logs (username, ip, method, path, status, resource_type, resource_id, details)
		VALUES ('system:agent-action','','JOB','agent_action',$1,'agent',$2,$3::jsonb)
	`, status, agentID, string(details))
	log.Printf("agent action %s → %s (%s): status=%d %s", action, target, agentID, status, message)
}

func postJSON(ctx context.Context, url string, payload any) error {
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("notification webhook: %v", err)
		return err
	}
	defer resp.Body.Close()
	return nil
}

func sendPagerDuty(ctx context.Context, cfg map[string]any, title, body, transition, deviceID string) error {
	key, _ := cfg["routing_key"].(string)
	if key == "" {
		return nil
	}
	action := "trigger"
	if transition == "recovered" {
		action = "resolve"
	}
	severity := "error"
	if transition == "warning" {
		severity = "warning"
	}
	payload := map[string]any{
		"routing_key": key, "event_action": action, "dedup_key": "netmonitor-" + deviceID,
		"payload": map[string]any{
			"summary": title + " - " + body, "source": "NetMonitor", "severity": severity,
			"custom_details": map[string]any{"status": transition, "device_id": deviceID},
		},
	}
	return postJSON(ctx, "https://events.pagerduty.com/v2/enqueue", payload)
}

func sendOpsgenie(ctx context.Context, cfg map[string]any, title, body, transition, deviceID string) error {
	key, _ := cfg["api_key"].(string)
	if key == "" {
		return nil
	}
	base := "https://api.opsgenie.com"
	if region, _ := cfg["region"].(string); strings.EqualFold(region, "eu") {
		base = "https://api.eu.opsgenie.com"
	}
	alias := "netmonitor-" + deviceID
	if transition == "recovered" {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v2/alerts/"+url.PathEscape(alias)+"/close", strings.NewReader(`{"note":"Recuperado"}`))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "GenieKey "+key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	payload := map[string]any{
		"message": title, "description": body, "alias": alias,
		"priority": "P1", "source": "NetMonitor",
	}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v2/alerts", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "GenieKey "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func sendSMS(ctx context.Context, cfg map[string]any, text string) error {
	sid, _ := cfg["account_sid"].(string)
	token, _ := cfg["auth_token"].(string)
	from, _ := cfg["from"].(string)
	to, _ := cfg["to"].(string)
	if sid == "" || token == "" || from == "" || to == "" {
		return nil
	}
	form := url.Values{"From": {from}, "To": {to}, "Body": {text}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.twilio.com/2010-04-01/Accounts/"+url.PathEscape(sid)+"/Messages.json",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(sid, token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func sendEmail(cfg map[string]any, subject, body string) error {
	host, _ := cfg["host"].(string)
	from, _ := cfg["from"].(string)
	to, _ := cfg["to"].(string)
	user, _ := cfg["username"].(string)
	pass, _ := cfg["password"].(string)
	port := 587
	if v, ok := cfg["port"].(float64); ok {
		port = int(v)
	}
	if host == "" || from == "" || to == "" {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	auth := smtp.PlainAuth("", user, pass, host)
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		from, to, subject, strings.ReplaceAll(body, "\n", "<br>")))
	return smtp.SendMail(addr, auth, from, []string{to}, msg)
}

func nullableUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func statusIcon(s string) string {
	switch s {
	case "down":
		return "🔴"
	case "warning":
		return "🟡"
	case "recovered", "up":
		return "🟢"
	case "unreachable":
		return "🟠"
	default:
		return "⚪"
	}
}
