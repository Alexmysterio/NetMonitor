package api

import (
	"strings"
	"testing"

	"github.com/netmonitor/netmonitor/internal/dude"
	"github.com/netmonitor/netmonitor/internal/secure"
)

func TestDudeSNMPProfileKey(t *testing.T) {
	if got := dudeSNMPSettingsKey("v2c", 161, "public"); got != "v2c\x00161\x00public" {
		t.Fatalf("unexpected settings key: %q", got)
	}
	p := dude.SNMPProfile{Name: " v2-public ", Version: "v2c", Port: 161, Community: "public"}
	if got := dudeSNMPProfileKey(p); got != "Dude · v2-public\x00v2c\x00161\x00public" {
		t.Fatalf("unexpected key: %q", got)
	}
	p.SourceID = "60"
	if got := dudeSNMPProfileKey(p); got != "Dude · v2-public [60]\x00v2c\x00161\x00public" {
		t.Fatalf("unexpected key with source ID: %q", got)
	}
}

func TestDudeSNMPProfileNameDeterministic(t *testing.T) {
	p := dude.SNMPProfile{SourceID: "60", Name: " v2-public ", Version: "v2c", Port: 161, Community: "public"}
	first := dudeSNMPProfileName(p)
	second := dudeSNMPProfileName(p)
	if first != second {
		t.Fatalf("name is not deterministic: %q != %q", first, second)
	}
	if first != "Dude · v2-public [60]" {
		t.Fatalf("unexpected name: %q", first)
	}
}

func TestDudeSNMPProfileNameKeepsSourceIDWhenTruncated(t *testing.T) {
	p := dude.SNMPProfile{
		SourceID: "source-123", Name: strings.Repeat("x", 200),
		Version: "v2c", Port: 161, Community: "public",
	}
	name := dudeSNMPProfileName(p)
	if len([]rune(name)) > dudeSNMPProfileNameMax {
		t.Fatalf("name too long: %d runes", len([]rune(name)))
	}
	if !strings.HasSuffix(name, " [source-123]") {
		t.Fatalf("source ID suffix was lost: %q", name)
	}
	if !strings.HasPrefix(name, dudeSNMPProfilePrefix) {
		t.Fatalf("prefix missing: %q", name)
	}
}

func TestDudeSNMPProfileHashedNameDiffersBySettings(t *testing.T) {
	secure.SetKey("test-key")
	base := dude.SNMPProfile{SourceID: "60", Name: "v2-public", Version: "v2c", Port: 161}
	public := base
	public.Community = "public"
	private := base
	private.Community = "private"
	firstName, err := dudeSNMPProfileHashedName(public)
	if err != nil {
		t.Fatal(err)
	}
	secondName, err := dudeSNMPProfileHashedName(private)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := dudeSNMPProfileHashedName(public)
	if err != nil {
		t.Fatal(err)
	}
	if firstName != repeated {
		t.Fatalf("hashed name is not stable for the same key and value: %q != %q", firstName, repeated)
	}
	if firstName == secondName {
		t.Fatalf("different settings produced the same name: %q", firstName)
	}
	if !strings.Contains(firstName, " #") || !strings.HasSuffix(firstName, " [60]") {
		t.Fatalf("unexpected hashed name: %q", firstName)
	}
	if len([]rune(firstName)) > dudeSNMPProfileNameMax {
		t.Fatalf("hashed name too long: %d runes", len([]rune(firstName)))
	}
}

func TestDudeSNMPProfileHashedNameReused(t *testing.T) {
	secure.SetKey("test-key")
	p := dude.SNMPProfile{SourceID: "60", Name: "v2-public", Version: "v2c", Port: 161, Community: "private"}
	base := dudeSNMPProfileName(p)
	hashed, err := dudeSNMPProfileHashedName(p)
	if err != nil {
		t.Fatal(err)
	}
	existing := map[string]dudeSNMPExistingProfile{
		strings.ToLower(base): {
			id: "conflict", name: base, version: "v2c", port: 161, community: "public", decoded: true,
		},
		strings.ToLower(hashed): {
			id: "profile-id", name: hashed, version: "v2c", port: 161, community: "private", decoded: true,
		},
	}
	name, profile, found, err := dudeSNMPProfileTargetName(p, existing)
	if err != nil {
		t.Fatal(err)
	}
	if !found || name != hashed || profile.id != "profile-id" || !dudeSNMPProfileMatches(p, profile) {
		t.Fatalf("hashed profile was not reused: name=%q found=%v profile=%+v", name, found, profile)
	}
}

func TestDudeSNMPSkipWarning(t *testing.T) {
	base := dude.SNMPProfile{SourceID: "60", Name: "v2-public", Version: "v2c", Community: "public"}
	if warning, skip := dudeSNMPSkipWarning(base); skip {
		t.Fatalf("valid profile was skipped: %q", warning)
	}

	empty := base
	empty.Community = ""
	warning, skip := dudeSNMPSkipWarning(empty)
	if !skip || warning != `Perfil SNMP "Dude · v2-public [60]" sin community para v2c; se omite.` {
		t.Fatalf("unexpected empty-community result: skip=%v warning=%q", skip, warning)
	}

	failed := base
	failed.CommunityDecryptFailed = true
	warning, skip = dudeSNMPSkipWarning(failed)
	if !skip || warning != `Perfil SNMP "Dude · v2-public [60]" omitido: no se pudo descifrar su community.` {
		t.Fatalf("unexpected decrypt-failure result: skip=%v warning=%q", skip, warning)
	}

	v3Empty := base
	v3Empty.Version = "v3"
	v3Empty.Community = ""
	if warning, skip := dudeSNMPSkipWarning(v3Empty); skip {
		t.Fatalf("v3 profile without secrets should be kept: %q", warning)
	}
}

func TestDudeDeviceSNMPProfileSourceID(t *testing.T) {
	profiles := []dude.SNMPProfile{
		{SourceID: "60", Name: "v2-public"},
		{SourceID: "61", Name: "Shared"},
	}
	cases := []struct {
		name   string
		device dude.Device
		want   string
	}{
		{"source id wins", dude.Device{SNMPProfileSourceID: "60", SNMPProfileName: "Shared"}, "60"},
		{"name fallback", dude.Device{SNMPProfileName: "v2-public"}, "60"},
		{"legacy name in source id", dude.Device{SNMPProfileSourceID: "v2-public", SNMPProfileName: "v2-public"}, "60"},
		{"missing profile", dude.Device{SNMPProfileSourceID: "nope", SNMPProfileName: "missing"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dudeDeviceSNMPProfileSourceID(tc.device, profiles); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDudeSelectionHelpers(t *testing.T) {
	if !dudeDeviceSelected(map[string]bool{}, "any") {
		t.Fatal("empty selection should mean all devices")
	}
	if dudeDeviceSelected(map[string]bool{"a": true}, "b") {
		t.Fatal("unselected device should be skipped")
	}
	if !dudeDeviceInSelectedMap(dude.Device{}, map[string]bool{}) {
		t.Fatal("device without a map should be importable")
	}
	if dudeDeviceInSelectedMap(dude.Device{SourceMapID: "m1"}, map[string]bool{}) {
		t.Fatal("device on an unselected map should be skipped")
	}
}
