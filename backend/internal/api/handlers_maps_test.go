package api

import "testing"

func TestMapCountsStatusAcknowledged(t *testing.T) {
	cases := []struct {
		name   string
		counts mapCounts
		want   string
	}{
		{name: "down wins", counts: mapCounts{Down: 1, Acknowledged: 2}, want: "down"},
		{name: "acknowledged after down and unreachable", counts: mapCounts{Acknowledged: 2, Warning: 1}, want: "acknowledged"},
		{name: "unreachable wins over acknowledged", counts: mapCounts{Unreachable: 1, Acknowledged: 2}, want: "unreachable"},
		{name: "warning when there is no ack", counts: mapCounts{Warning: 1, Up: 2}, want: "warning"},
		{name: "up", counts: mapCounts{Up: 2}, want: "up"},
	}
	for _, tc := range cases {
		if got := tc.counts.Status(); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMapSummaryLabelIncludesAcknowledged(t *testing.T) {
	got := mapSummaryLabel(mapCounts{Up: 3, Warning: 1, Down: 2, Acknowledged: 4})
	want := "3 up · 1 warn · 2 down · 4 ack"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
