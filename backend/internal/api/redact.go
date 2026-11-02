package api

import "strings"

var sensitiveConfigKeys = map[string]bool{
	"bot_token": true, "webhook_secret": true, "secret": true, "token": true,
	"password": true, "pass": true, "api_key": true, "apikey": true,
	"routing_key": true, "access_key": true, "secret_key": true,
	"private_key": true, "client_secret": true, "auth_token": true,
}

func redactConfig(cfg map[string]any) (map[string]any, bool) {
	out := make(map[string]any, len(cfg))
	redacted := false
	for key, value := range cfg {
		lower := strings.ToLower(key)
		if sensitiveConfigKeys[lower] || strings.Contains(lower, "secret") ||
			strings.Contains(lower, "password") || strings.Contains(lower, "token") {
			if text, ok := value.(string); !ok || text != "" {
				out[key] = "••••••"
				redacted = true
				continue
			}
		}
		out[key] = value
	}
	return out, redacted
}
