package schedule

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, raw string) Window {
	t.Helper()
	w, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return w
}

func TestAlways(t *testing.T) {
	for _, raw := range []string{"", "{}", `{"always":true}`} {
		w := mustParse(t, raw)
		if !w.Allows(time.Date(2026, 10, 29, 3, 0, 0, 0, time.UTC)) {
			t.Fatalf("%q should always allow", raw)
		}
	}
}

func TestWeekdayWindow(t *testing.T) {
	w := mustParse(t, `{"days":[1,2,3,4,5],"from":"09:00","to":"18:00","timezone":"Europe/Madrid"}`)
	loc, _ := time.LoadLocation("Europe/Madrid")
	thursday := time.Date(2026, 10, 29, 10, 30, 0, 0, loc)
	if !w.Allows(thursday) {
		t.Fatal("Thursday 10:30 should be inside the window")
	}
	if w.Allows(time.Date(2026, 10, 29, 18, 0, 0, 0, loc)) {
		t.Fatal("18:00 is the exclusive end")
	}
	if w.Allows(time.Date(2026, 10, 31, 10, 30, 0, 0, loc)) {
		t.Fatal("Saturday should be excluded")
	}
}

func TestOvernightWindow(t *testing.T) {
	w := mustParse(t, `{"from":"22:00","to":"06:00","timezone":"UTC"}`)
	if !w.Allows(time.Date(2026, 10, 29, 23, 0, 0, 0, time.UTC)) {
		t.Fatal("23:00 should be inside an overnight window")
	}
	if !w.Allows(time.Date(2026, 10, 29, 5, 59, 0, 0, time.UTC)) {
		t.Fatal("05:59 should be inside an overnight window")
	}
	if w.Allows(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("12:00 should be outside an overnight window")
	}
}

func TestHolidays(t *testing.T) {
	w := mustParse(t, `{"always":true,"holidays":["2026-12-25"],"timezone":"Europe/Madrid"}`)
	loc, _ := time.LoadLocation("Europe/Madrid")
	if w.Allows(time.Date(2026, 12, 25, 10, 0, 0, 0, loc)) {
		t.Fatal("holiday must be excluded")
	}
	if !w.Allows(time.Date(2026, 12, 24, 10, 0, 0, 0, loc)) {
		t.Fatal("non-holiday should be allowed")
	}
	w = mustParse(t, `{"days":[1,2,3,4,5],"from":"09:00","to":"18:00","holidays":["2026-10-29"],"timezone":"Europe/Madrid"}`)
	if w.Allows(time.Date(2026, 10, 29, 10, 0, 0, 0, loc)) {
		t.Fatal("holiday must override an otherwise valid weekday window")
	}
}

func TestInvalid(t *testing.T) {
	cases := []string{
		`{"from":"09:00"}`,
		`{"from":"25:00","to":"10:00"}`,
		`{"from":"09:00","to":"18:00","days":[0]}`,
		`{"from":"09:00","to":"18:00","timezone":"Mars/Olympus"}`,
		`{"always":true,"holidays":["25/12/2026"]}`,
		`not-json`,
	}
	for _, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("Parse(%q) should fail", raw)
		}
	}
}

func TestString(t *testing.T) {
	w := mustParse(t, `{"days":[1,5],"from":"09:00","to":"18:00","timezone":"Europe/Madrid"}`)
	if got := w.String(); got != "LV 09:00-18:00 (Europe/Madrid)" {
		t.Fatalf("String() = %q", got)
	}
}
