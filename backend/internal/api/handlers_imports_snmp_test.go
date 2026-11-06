package api

import (
	"strings"
	"testing"

	"github.com/netmonitor/netmonitor/internal/dude"
)

func TestDudeSNMPProfileKey(t *testing.T) {
	p := dude.SNMPProfile{Name: " v2-public ", Version: "v2c", Port: 161, Community: "public"}
	if got := dudeSNMPProfileKey(p); got != "Dude · v2-public\x00v2c\x00161\x00public" {
		t.Fatalf("unexpected key: %q", got)
	}
}

func TestDudeSNMPExistingKey(t *testing.T) {
	source := dude.SNMPProfile{Name: "v2-public", Version: "v2c", Port: 161, Community: "public"}
	if got, want := dudeSNMPExistingKey("Dude · v2-public", "v2c", 161, "public"), dudeSNMPProfileKey(source); got != want {
		t.Fatalf("unexpected existing key: %q, want %q", got, want)
	}
}

func TestUniqueDudeSNMPName(t *testing.T) {
	used := map[string]bool{"dude · v2-public": true}
	if got := uniqueDudeSNMPName("Dude · v2-public", used); got != "Dude · v2-public (2)" {
		t.Fatalf("unexpected name: %q", got)
	}
	used["dude · v2-public (2)"] = true
	if got := uniqueDudeSNMPName("Dude · v2-public", used); got != "Dude · v2-public (3)" {
		t.Fatalf("unexpected name: %q", got)
	}
	long := uniqueDudeSNMPName(strings.Repeat("x", 120), map[string]bool{})
	if len([]rune(long)) != dudeSNMPProfileNameMax {
		t.Fatalf("long name was not truncated: %d runes", len([]rune(long)))
	}
}
