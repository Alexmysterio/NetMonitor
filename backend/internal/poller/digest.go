package poller

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

func (p *Poller) digestLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.sendDueDigests(ctx)
		}
	}
}

func (p *Poller) sendDueDigests(ctx context.Context) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, name, interval_sec, channel_ids
		FROM digest_rules
		WHERE is_enabled=TRUE
		  AND (last_sent_at IS NULL OR last_sent_at + (interval_sec * interval '1 second') <= NOW())
	`)
	if err != nil {
		log.Printf("digest rules: %v", err)
		return
	}
	type digestRule struct {
		id, name string
		interval int
		channels []string
	}
	var rules []digestRule
	for rows.Next() {
		var r digestRule
		if rows.Scan(&r.id, &r.name, &r.interval, &r.channels) == nil && len(r.channels) > 0 {
			rules = append(rules, r)
		}
	}
	rows.Close()
	for _, r := range rules {
		tag, err := p.pool.Exec(ctx, `
			UPDATE digest_rules SET last_sent_at=NOW()
			WHERE id=$1 AND (last_sent_at IS NULL OR last_sent_at + (interval_sec * interval '1 second') <= NOW())
		`, r.id)
		if err != nil || tag.RowsAffected() == 0 {
			continue
		}
		p.sendDigest(ctx, r.id, r.name, r.channels)
	}
}

func (p *Poller) sendDigest(ctx context.Context, id, name string, channelIDs []string) {
	rows, err := p.pool.Query(ctx, `
		SELECT d.name, host(d.primary_ip), o.severity, o.started_at, COALESCE(o.error_message,'')
		FROM outages o JOIN devices d ON d.id=o.device_id
		WHERE o.ended_at IS NULL
		ORDER BY o.started_at ASC
		LIMIT 50
	`)
	if err != nil {
		log.Printf("digest outages: %v", err)
		return
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var device, ip, severity, message string
		var started time.Time
		if rows.Scan(&device, &ip, &severity, &started, &message) == nil {
			dur := time.Since(started).Round(time.Minute)
			lines = append(lines, fmt.Sprintf("• %s (%s) · %s · %s", device, ip, strings.ToUpper(severity), dur))
		}
	}
	if len(lines) == 0 {
		_, _ = p.pool.Exec(ctx, `UPDATE digest_rules SET last_sent_at=NOW() WHERE id=$1`, id)
		return
	}
	channels := p.loadAlertChannels(ctx, channelIDs)
	if len(channels) == 0 {
		return
	}
	grouped := make([]alertChannel, len(channels))
	for i, c := range channels {
		c.title = fmt.Sprintf("[DIGEST] %d incidencias activas", len(lines))
		c.body = "{{Event.Message}}"
		grouped[i] = c
	}
	summary := fmt.Sprintf("%s — %d incidencias activas\n%s", name, len(lines), strings.Join(lines, "\n"))
	p.sendChannels(ctx, grouped, "", "", "Resumen", "", "Resumen", "digest", summary)
	_, _ = p.pool.Exec(ctx, `UPDATE digest_rules SET last_sent_at=NOW() WHERE id=$1`, id)
}
