package poller

import "testing"

func TestFormatBps(t *testing.T) {
	cases := []struct {
		bps  float64
		want string
	}{
		{0, "0 bps"},
		{999, "999 bps"},
		{1500, "1.5 Kbps"},
		{1_500_000, "1.5 Mbps"},
		{12_361_348, "12.4 Mbps"},
		{150_000_000, "150 Mbps"},
		{1_500_000_000, "1.5 Gbps"},
	}
	for _, tc := range cases {
		if got := formatBps(tc.bps); got != tc.want {
			t.Errorf("formatBps(%v) = %q, want %q", tc.bps, got, tc.want)
		}
	}
}
