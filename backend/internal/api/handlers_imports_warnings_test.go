package api

import "testing"

func TestDudePlural(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "equipos"},
		{1, "equipo"},
		{2, "equipos"},
	}
	for _, tc := range cases {
		if got := dudePlural(tc.n, "equipo", "equipos"); got != tc.want {
			t.Fatalf("dudePlural(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
