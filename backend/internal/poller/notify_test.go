package poller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAlertLimiter(t *testing.T) {
	l := &alertLimiter{limit: 2}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	if !l.allow(now) || !l.allow(now.Add(10*time.Second)) {
		t.Fatal("first two sends should be allowed")
	}
	if l.allow(now.Add(20 * time.Second)) {
		t.Fatal("third send in the same minute should be blocked")
	}
	if !l.allow(now.Add(time.Minute)) {
		t.Fatal("a new window should allow sends again")
	}
}

func TestAlertLimiterDisabled(t *testing.T) {
	l := &alertLimiter{}
	for i := 0; i < 1000; i++ {
		if !l.allow(time.Now()) {
			t.Fatal("limit 0 must allow every send")
		}
	}
}

func TestSendGroupedWebhook(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &Poller{}
	ch := alertChannel{
		id: "test", kind: "webhook", cfg: `{"url":"` + srv.URL + `"}`,
		title: "[{{Status}}] 2 equipos en Sede", body: "{{Event.Message}}",
	}
	p.sendChannels(context.Background(), []alertChannel{ch}, "", "", "2 equipos", "", "Sede", "down",
		"Sede — 2 equipos afectados\n• A (10.0.0.1): timeout")

	if got, _ := payload["title"].(string); !strings.Contains(got, "[DOWN] 2 equipos en Sede") {
		t.Fatalf("unexpected title: %v", payload["title"])
	}
	if got, _ := payload["body"].(string); !strings.Contains(got, "2 equipos afectados") {
		t.Fatalf("unexpected body: %v", payload["body"])
	}
}
