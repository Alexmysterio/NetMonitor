package poller

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/netmonitor/netmonitor/internal/schedule"
)

func (p *Poller) holidayLoop(ctx context.Context) {
	load := func() {
		var raw string
		if err := p.pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key='global_holidays'`).Scan(&raw); err != nil {
			return
		}
		var days []string
		if json.Unmarshal([]byte(raw), &days) != nil {
			return
		}
		set := map[string]bool{}
		for _, day := range days {
			set[day] = true
		}
		p.holidayMu.Lock()
		p.holidays = set
		p.holidayMu.Unlock()
	}
	load()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			load()
		}
	}
}

func (p *Poller) isGlobalHoliday(now time.Time) bool {
	day := now.In(time.Local).Format("2006-01-02")
	p.holidayMu.RLock()
	defer p.holidayMu.RUnlock()
	return p.holidays[day]
}

func (p *Poller) scheduleAllows(raw string, now time.Time) bool {
	if p.isGlobalHoliday(now) {
		return false
	}
	w, err := schedule.Parse(raw)
	if err != nil {
		log.Printf("alert rule schedule: %v", err)
		return true
	}
	return w.Allows(now)
}
