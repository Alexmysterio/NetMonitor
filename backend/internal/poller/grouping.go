package poller

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type groupedEvent struct {
	deviceID string
	device   string
	ip       string
	reason   string
}

type groupedAlert struct {
	rule       alertRule
	mapID      string
	mapName    string
	transition string
	events     map[string]groupedEvent
}

// enqueueGrouped buffers alerts for rules with group_by=map. The first event
// starts a timer; when it fires, one digest is sent per channel instead of one
// message per device. Rules with grouping do not send repeats.
func (p *Poller) enqueueGrouped(ctx context.Context, rule alertRule, scope, scopeName, deviceID, device, ip, transition, reason string) {
	window := rule.groupWindow
	if window <= 0 {
		window = 60
	}
	key := rule.id + "|" + scope + "|" + transition

	p.groupMu.Lock()
	g := p.groups[key]
	if g == nil {
		g = &groupedAlert{
			rule: rule, mapID: scope, mapName: scopeName, transition: transition,
			events: map[string]groupedEvent{},
		}
		p.groups[key] = g
		delay := time.Duration(window+rule.delaySec) * time.Second
		time.AfterFunc(delay, func() { p.flushGroup(ctx, key) })
	}
	g.events[deviceID] = groupedEvent{deviceID: deviceID, device: device, ip: ip, reason: reason}
	p.groupMu.Unlock()
}

func (p *Poller) flushGroup(ctx context.Context, key string) {
	p.groupMu.Lock()
	g := p.groups[key]
	delete(p.groups, key)
	p.groupMu.Unlock()
	if g == nil || ctx.Err() != nil {
		return
	}
	if !p.scheduleAllows(g.rule.schedule, time.Now()) {
		return
	}

	var lines []string
	total := 0
	for _, e := range g.events {
		if g.transition != "flapping" && !p.alertStillRelevant(ctx, e.deviceID, g.transition) {
			continue
		}
		total++
		if len(lines) >= 40 {
			continue
		}
		reason := strings.TrimSpace(e.reason)
		if reason == "" {
			lines = append(lines, fmt.Sprintf("• %s (%s)", e.device, e.ip))
		} else {
			lines = append(lines, fmt.Sprintf("• %s (%s): %s", e.device, e.ip, reason))
		}
	}
	if total == 0 {
		return
	}
	if total > len(lines) {
		lines = append(lines, fmt.Sprintf("… y %d equipos más", total-len(lines)))
	}

	channels := p.loadAlertChannels(ctx, g.rule.channelIDs)
	if len(channels) == 0 {
		return
	}
	grouped := make([]alertChannel, len(channels))
	for i, c := range channels {
		c.title = fmt.Sprintf("[{{Status}}] %d equipos en %s", total, g.mapName)
		c.body = "{{Event.Message}}"
		grouped[i] = c
	}
	summary := fmt.Sprintf("%s — %d equipos afectados\n%s", g.mapName, total, strings.Join(lines, "\n"))
	p.sendChannels(ctx, grouped, "", "", fmt.Sprintf("%d equipos", total), "", g.mapName, g.transition, summary)
}
