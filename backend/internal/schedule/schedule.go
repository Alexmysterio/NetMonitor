// Package schedule evaluates the active_schedule window of alert rules.
//
// A window is stored as JSONB in alert_rules.active_schedule:
//
//	{"always": true}
//	{"days": [1,2,3,4,5], "from": "09:00", "to": "18:00", "timezone": "Europe/Madrid"}
//
// Days use ISO numbering (1 = Monday ... 7 = Sunday). An empty days array means
// every day. Overnight windows such as 22:00-06:00 are supported.
package schedule

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Window is a parsed alert-rule schedule.
type Window struct {
	Always   bool     `json:"always"`
	Days     []int    `json:"days,omitempty"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Timezone string   `json:"timezone,omitempty"`
	Holidays []string `json:"holidays,omitempty"`

	loc      *time.Location
	fromMin  int
	toMin    int
	holidays map[string]bool
}

// Parse validates and parses a stored schedule. Empty or always schedules are
// accepted. Invalid schedules return an error so the API can reject them.
func Parse(raw string) (Window, error) {
	if strings.TrimSpace(raw) == "" {
		return Window{Always: true}, nil
	}
	var w Window
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		return Window{}, fmt.Errorf("horario inválido: %w", err)
	}
	if w.Always || (w.From == "" && w.To == "" && len(w.Days) == 0 && len(w.Holidays) == 0) {
		w.Always = true
		if len(w.Holidays) == 0 {
			return w, nil
		}
	}
	if !w.Always {
		if w.From == "" || w.To == "" {
			return Window{}, fmt.Errorf("el horario requiere hora de inicio y de fin")
		}
		var err error
		if w.fromMin, err = parseClock(w.From); err != nil {
			return Window{}, fmt.Errorf("hora de inicio: %w", err)
		}
		if w.toMin, err = parseClock(w.To); err != nil {
			return Window{}, fmt.Errorf("hora de fin: %w", err)
		}
	}
	for _, d := range w.Days {
		if d < 1 || d > 7 {
			return Window{}, fmt.Errorf("día %d inválido: usa 1 (lunes) a 7 (domingo)", d)
		}
	}
	if w.Timezone == "" {
		w.loc = time.Local
	} else {
		loc, err := time.LoadLocation(w.Timezone)
		if err != nil {
			return Window{}, fmt.Errorf("zona horaria inválida %q", w.Timezone)
		}
		w.loc = loc
	}
	w.holidays = map[string]bool{}
	for i, raw := range w.Holidays {
		day := strings.TrimSpace(raw)
		if _, err := time.Parse("2006-01-02", day); err != nil {
			return Window{}, fmt.Errorf("festivo %q inválido: usa YYYY-MM-DD", raw)
		}
		w.Holidays[i] = day
		w.holidays[day] = true
	}
	return w, nil
}

// Allows reports whether now falls inside the window. Invalid internal state
// fails open so a malformed rule never silences critical alerts.
func (w Window) Allows(now time.Time) bool {
	if w.loc == nil {
		return true
	}
	t := now.In(w.loc)
	if len(w.holidays) > 0 && w.holidays[t.Format("2006-01-02")] {
		return false
	}
	if w.Always {
		return true
	}
	day := int(t.Weekday())
	if day == 0 {
		day = 7
	}
	if len(w.Days) > 0 {
		found := false
		for _, d := range w.Days {
			if d == day {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	mins := t.Hour()*60 + t.Minute()
	if w.fromMin == w.toMin {
		return true
	}
	if w.fromMin < w.toMin {
		return mins >= w.fromMin && mins < w.toMin
	}
	return mins >= w.fromMin || mins < w.toMin
}

// String returns a compact human-readable summary used in logs and APIs.
func (w Window) String() string {
	holidays := ""
	if len(w.Holidays) > 0 {
		label := "festivos"
		if len(w.Holidays) == 1 {
			label = "festivo"
		}
		holidays = fmt.Sprintf(" · %d %s", len(w.Holidays), label)
	}
	if w.Always {
		return "Siempre" + holidays
	}
	days := "Todos los días"
	if len(w.Days) > 0 {
		names := []string{"", "L", "M", "X", "J", "V", "S", "D"}
		var parts []string
		for _, d := range w.Days {
			if d >= 1 && d <= 7 {
				parts = append(parts, names[d])
			}
		}
		days = strings.Join(parts, "")
	}
	zone := w.Timezone
	if zone == "" {
		zone = "local"
	}
	return fmt.Sprintf("%s %s-%s (%s)%s", days, w.From, w.To, zone, holidays)
}

func parseClock(v string) (int, error) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("usa el formato HH:MM")
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("hora %q fuera de rango", parts[0])
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("minutos %q fuera de rango", parts[1])
	}
	return h*60 + m, nil
}
