package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/secure"
	probing "github.com/prometheus-community/pro-bing"
)

func (s *Server) telegramWebhook(w http.ResponseWriter, r *http.Request) {
	channelID := chi.URLParam(r, "id")
	var channelType, configRaw string
	err := s.pool.QueryRow(r.Context(), `
		SELECT channel_type, config::text FROM notification_channels
		WHERE id=$1 AND is_enabled=TRUE
	`, channelID).Scan(&channelType, &configRaw)
	if err != nil || channelType != "telegram" {
		writeError(w, http.StatusNotFound, "canal Telegram no encontrado")
		return
	}
	cfg := map[string]any{}
	_ = json.Unmarshal([]byte(secure.UnwrapJSON(configRaw)), &cfg)
	secret, _ := cfg["webhook_secret"].(string)
	provided := r.URL.Query().Get("secret")
	if secret == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
		writeError(w, http.StatusForbidden, "secreto de webhook inválido")
		return
	}
	expectedChat, _ := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(cfg["chat_id"])), 10, 64)
	if expectedChat == 0 {
		if number, ok := cfg["chat_id"].(float64); ok {
			expectedChat = int64(number)
		}
	}
	if expectedChat == 0 {
		writeError(w, http.StatusForbidden, "el canal no tiene chat_id configurado")
		return
	}
	token, _ := cfg["bot_token"].(string)
	if token == "" {
		writeError(w, http.StatusBadRequest, "el canal no tiene bot_token")
		return
	}

	var update struct {
		CallbackQuery *struct {
			ID      string                    `json:"id"`
			From    struct{ Username string } `json:"from"`
			Data    string                    `json:"data"`
			Message struct {
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"callback_query"`
		Message *struct {
			Text string                    `json:"text"`
			From struct{ Username string } `json:"from"`
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	}
	if err := decodeBody(r, &update); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	actualChat := int64(0)
	if update.Message != nil {
		actualChat = update.Message.Chat.ID
	}
	cb := update.CallbackQuery
	if cb != nil {
		actualChat = cb.Message.Chat.ID
	}
	if actualChat != expectedChat {
		writeError(w, http.StatusForbidden, "chat de Telegram no autorizado")
		return
	}
	if update.Message != nil && strings.HasPrefix(strings.TrimSpace(update.Message.Text), "/") {
		s.telegramCommand(r.Context(), token, update.Message.Chat.ID, update.Message.From.Username, update.Message.Text)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if cb == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	parts := strings.SplitN(cb.Data, ":", 2)
	if len(parts) != 2 {
		_, _ = telegramCall(token, "answerCallbackQuery", map[string]any{"callback_query_id": cb.ID, "text": "Acción no reconocida"})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	action, deviceID := parts[0], parts[1]
	var name, ip string
	if err := s.pool.QueryRow(r.Context(), `SELECT name, host(primary_ip) FROM devices WHERE id=$1`, deviceID).Scan(&name, &ip); err != nil {
		_, _ = telegramCall(token, "answerCallbackQuery", map[string]any{"callback_query_id": cb.ID, "text": "Equipo no encontrado"})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	answer := ""
	switch action {
	case "ack":
		_, _ = s.pool.Exec(r.Context(), `
			UPDATE devices SET is_acknowledged=TRUE, ack_by=$2, ack_note='Telegram', updated_at=NOW()
			WHERE id=$1
		`, deviceID, cb.From.Username)
		_, _ = s.pool.Exec(r.Context(), `
			UPDATE outages SET acknowledged=TRUE, acknowledged_by=$2, acknowledged_at=NOW()
			WHERE device_id=$1 AND ended_at IS NULL
		`, deviceID, cb.From.Username)
		if s.notifyAck != nil {
			s.notifyAck(r.Context(), deviceID)
		}
		answer = "✅ Incidencia reconocida por " + cb.From.Username
	case "mute":
		_, _ = s.pool.Exec(r.Context(), `
			UPDATE devices SET maintenance_until=NOW()+interval '1 hour',
			 maintenance_note='Silenciado desde Telegram', updated_at=NOW()
			WHERE id=$1
		`, deviceID)
		answer = "🔇 Avisos silenciados 1 hora"
	case "ping":
		ok, rtt, err := quickPing(ip)
		if err != nil || !ok {
			answer = "🏓 " + name + ": sin respuesta"
		} else {
			answer = fmt.Sprintf("🏓 %s: %.1f ms", name, rtt)
		}
	default:
		answer = "Acción no reconocida"
	}
	if action == "ack" || action == "mute" {
		details, _ := json.Marshal(map[string]any{"action": action, "device_id": deviceID, "telegram_user": cb.From.Username})
		_, _ = s.pool.Exec(r.Context(), `
			INSERT INTO audit_logs (username, ip, method, path, status, resource_type, resource_id, details)
			VALUES ($1,'','TELEGRAM','/api/v1/telegram/webhook',200,'device',$2,$3::jsonb)
		`, "telegram:"+cb.From.Username, deviceID, string(details))
	}
	_, _ = telegramCall(token, "answerCallbackQuery", map[string]any{"callback_query_id": cb.ID, "text": answer, "show_alert": false})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func quickPing(host string) (bool, float64, error) {
	pinger, err := probing.NewPinger(host)
	if err != nil {
		return false, 0, err
	}
	pinger.Count = 3
	pinger.Timeout = 3 * time.Second
	pinger.SetPrivileged(true)
	if err := pinger.Run(); err != nil {
		return false, 0, err
	}
	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return false, 0, nil
	}
	return true, float64(stats.AvgRtt.Microseconds()) / 1000, nil
}

func (s *Server) setupTelegramWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var channelType, configRaw string
	err := s.pool.QueryRow(r.Context(), `
		SELECT channel_type, config::text FROM notification_channels WHERE id=$1
	`, id).Scan(&channelType, &configRaw)
	if err != nil || channelType != "telegram" {
		writeError(w, http.StatusNotFound, "canal Telegram no encontrado")
		return
	}
	cfg := map[string]any{}
	_ = json.Unmarshal([]byte(secure.UnwrapJSON(configRaw)), &cfg)
	token, _ := cfg["bot_token"].(string)
	if token == "" {
		writeError(w, http.StatusBadRequest, "el canal no tiene bot_token")
		return
	}
	secret, _ := cfg["webhook_secret"].(string)
	if secret == "" {
		buf := make([]byte, 24)
		_, _ = rand.Read(buf)
		secret = hex.EncodeToString(buf)
		cfg["webhook_secret"] = secret
		encoded, _ := json.Marshal(cfg)
		wrapped, err := secure.WrapJSON(string(encoded))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := s.pool.Exec(r.Context(), `UPDATE notification_channels SET config=$2::jsonb WHERE id=$1`, id, wrapped); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	webhookURL := strings.TrimRight(s.cfg.PublicURL, "/") + "/api/v1/telegram/webhook/" + id + "?secret=" + secret
	if !strings.HasPrefix(strings.ToLower(webhookURL), "https://") {
		writeError(w, http.StatusBadRequest, "los botones interactivos requieren una URL pública HTTPS. Configura PUBLIC_URL con tu dominio HTTPS (por ejemplo https://noc.tudominio.com) y reinicia NetMonitor")
		return
	}
	if _, err := telegramCall(token, "setWebhook", map[string]any{"url": webhookURL, "allowed_updates": []string{"callback_query"}}); err != nil {
		writeError(w, http.StatusBadGateway, "Telegram rechazó el webhook: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"webhook_url": webhookURL}})
}

func (s *Server) telegramCommand(ctx context.Context, token string, chatID int64, username, text string) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return
	}
	cmd := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	switch cmd {
	case "start", "help":
		_ = telegramSend(token, chatID, "NetMonitor\n/status — resumen de red\n/ack <equipo o IP> — reconocer incidencia")
	case "status":
		var up, warning, down, unreachable, active int
		_ = s.pool.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE status='up'),
			       count(*) FILTER (WHERE status='warning'),
			       count(*) FILTER (WHERE status IN ('down','acknowledged')),
			       count(*) FILTER (WHERE status='unreachable')
			FROM devices
		`).Scan(&up, &warning, &down, &unreachable)
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM outages WHERE ended_at IS NULL`).Scan(&active)
		msg := fmt.Sprintf("Estado de la red\n🟢 %d up · 🟡 %d warning · 🔴 %d down · 🟠 %d unreachable\nIncidencias activas: %d", up, warning, down, unreachable, active)
		rows, err := s.pool.Query(ctx, `
			SELECT d.name, host(d.primary_ip), o.severity
			FROM outages o JOIN devices d ON d.id=o.device_id
			WHERE o.ended_at IS NULL ORDER BY o.started_at ASC LIMIT 10
		`)
		if err == nil {
			for rows.Next() {
				var name, ip, sev string
				if rows.Scan(&name, &ip, &sev) == nil {
					msg += fmt.Sprintf("\n• %s (%s) %s", name, ip, strings.ToUpper(sev))
				}
			}
			rows.Close()
		}
		_ = telegramSend(token, chatID, msg)
	case "ack":
		if len(fields) < 2 {
			_ = telegramSend(token, chatID, "Uso: /ack <nombre o IP>")
			return
		}
		q := strings.Join(fields[1:], " ")
		var id, name, ip string
		err := s.pool.QueryRow(ctx, `
			SELECT id, name, host(primary_ip) FROM devices
			WHERE name ILIKE '%'||$1||'%' OR host(primary_ip)=$1
			ORDER BY name LIMIT 1
		`, q).Scan(&id, &name, &ip)
		if err != nil {
			_ = telegramSend(token, chatID, "Equipo no encontrado: "+q)
			return
		}
		_, _ = s.pool.Exec(ctx, `
			UPDATE devices SET is_acknowledged=TRUE, ack_by=$2, ack_note='Telegram', updated_at=NOW()
			WHERE id=$1
		`, id, username)
		_, _ = s.pool.Exec(ctx, `
			UPDATE outages SET acknowledged=TRUE, acknowledged_by=$2, acknowledged_at=NOW()
			WHERE device_id=$1 AND ended_at IS NULL
		`, id, username)
		if s.notifyAck != nil {
			s.notifyAck(ctx, id)
		}
		_ = telegramSend(token, chatID, fmt.Sprintf("✅ Reconocido: %s (%s)", name, ip))
	}
}

func telegramSend(token string, chatID int64, text string) error {
	_, err := telegramCall(token, "sendMessage", map[string]any{"chat_id": chatID, "text": text, "disable_web_page_preview": true})
	return err
}

func telegramCall(token, method string, payload any) (map[string]any, error) {
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://api.telegram.org/bot"+token+"/"+method, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if ok, _ := out["ok"].(bool); !ok {
		if desc, _ := out["description"].(string); desc != "" {
			return out, fmt.Errorf("%s", desc)
		}
		return out, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return out, nil
}
