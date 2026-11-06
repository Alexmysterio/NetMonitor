package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/netmonitor/netmonitor/internal/secure"
)

func TestDudeCredentialKey(t *testing.T) {
	cases := []struct {
		username string
		password string
		want     string
	}{
		{"admin", "secret", "admin\x00secret"},
		{"admin", "", "admin\x00"},
		{"", "", "\x00"},
		{"routeros", "p@ss|word", "routeros\x00p@ss|word"},
	}
	for _, tc := range cases {
		if got := dudeCredentialKey(tc.username, tc.password); got != tc.want {
			t.Fatalf("dudeCredentialKey(%q, %q) = %q, want %q", tc.username, tc.password, got, tc.want)
		}
		if got := dudeCredentialSettingsKey(tc.username, tc.password); got != tc.want {
			t.Fatalf("dudeCredentialSettingsKey(%q, %q) = %q, want %q", tc.username, tc.password, got, tc.want)
		}
	}
}

func TestGroupDudeCredentials(t *testing.T) {
	devices := []dudeCredentialDevice{
		{SourceID: "src-b", DeviceID: "dev-b", DeviceName: "Zulu", Username: "admin", Password: "secret"},
		{SourceID: "src-a", DeviceID: "dev-a", DeviceName: "alpha", Username: "admin", Password: "secret"},
		{SourceID: "src-c", DeviceID: "dev-c", DeviceName: "Bravo", Username: "admin", Password: "other"},
		{SourceID: "src-d", DeviceID: "dev-d", DeviceName: "Skipped", Username: "   ", Password: "x"},
		{SourceID: "src-e", DeviceID: "dev-e", DeviceName: "Empty", Username: "", Password: "y"},
	}
	groups := groupDudeCredentials(devices)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d: %+v", len(groups), groups)
	}
	secret := groups[0]
	if secret.Username != "admin" || secret.Password != "secret" {
		t.Fatalf("unexpected first group credentials: %+v", secret)
	}
	if got := []string{secret.Devices[0].SourceID, secret.Devices[1].SourceID}; !reflect.DeepEqual(got, []string{"src-a", "src-b"}) {
		t.Fatalf("devices were not grouped and sorted: %v", got)
	}
	other := groups[1]
	if other.Password != "other" || len(other.Devices) != 1 {
		t.Fatalf("different passwords were not split into separate groups: %+v", other)
	}
}

func TestGroupDudeCredentialsDeterministicOrder(t *testing.T) {
	devices := []dudeCredentialDevice{
		{SourceID: "src-c", DeviceName: "Charlie", Username: "bob", Password: "one"},
		{SourceID: "src-a", DeviceName: "Alpha", Username: "bob", Password: "one"},
		{SourceID: "src-b", DeviceName: "Bravo", Username: "alice", Password: "two"},
		{SourceID: "src-d", DeviceName: "Delta", Username: "alice", Password: "three"},
	}
	reversed := make([]dudeCredentialDevice, len(devices))
	for i := range devices {
		reversed[len(devices)-1-i] = devices[i]
	}
	first := groupDudeCredentials(devices)
	second := groupDudeCredentials(reversed)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("grouping is not deterministic:\nfirst:  %+v\nsecond: %+v", first, second)
	}
	if first[0].Devices[0].DeviceName != "Alpha" {
		t.Fatalf("groups should be ordered by first device name, got %q", first[0].Devices[0].DeviceName)
	}
}

func TestGroupDudeCredentialsTrimsUsername(t *testing.T) {
	groups := groupDudeCredentials([]dudeCredentialDevice{
		{SourceID: "a", DeviceID: "1", DeviceName: "A", Username: "  admin  ", Password: "secret"},
		{SourceID: "b", DeviceID: "2", DeviceName: "B", Username: "admin", Password: "secret"},
	})
	if len(groups) != 1 {
		t.Fatalf("expected trimmed usernames to share a group, got %d groups", len(groups))
	}
	if groups[0].Username != "admin" || len(groups[0].Devices) != 2 {
		t.Fatalf("unexpected group: %+v", groups[0])
	}
}

func TestDudeCredentialNameDeterministic(t *testing.T) {
	group := dudeCredentialGroup{
		Username: "admin",
		Password: "secret",
		Devices: []dudeCredentialDevice{
			{SourceID: "60", DeviceName: " RouterOS Edge ", Username: "admin", Password: "secret"},
		},
	}
	first := dudeCredentialBaseName(group)
	second := dudeCredentialBaseName(group)
	if first != second {
		t.Fatalf("name is not deterministic: %q != %q", first, second)
	}
	if first != "Dude · RouterOS Edge [60]" {
		t.Fatalf("unexpected name: %q", first)
	}
	if !strings.HasPrefix(first, dudeCredentialPrefix) {
		t.Fatalf("prefix missing: %q", first)
	}
}

func TestDudeCredentialNameFallback(t *testing.T) {
	group := dudeCredentialGroup{
		Username: "admin",
		Password: "secret",
		Devices:  []dudeCredentialDevice{{SourceID: "", DeviceName: "   "}},
	}
	if got := dudeCredentialBaseName(group); got != "Dude · RouterOS" {
		t.Fatalf("unexpected fallback name: %q", got)
	}
}

func TestDudeCredentialNameKeepsSourceIDWhenTruncated(t *testing.T) {
	group := dudeCredentialGroup{
		Username: "admin",
		Password: "secret",
		Devices: []dudeCredentialDevice{
			{SourceID: "source-123", DeviceName: strings.Repeat("x", 200)},
		},
	}
	name := dudeCredentialBaseName(group)
	if len([]rune(name)) > dudeCredentialNameMax {
		t.Fatalf("name too long: %d runes", len([]rune(name)))
	}
	if !strings.HasSuffix(name, " [source-123]") {
		t.Fatalf("source ID suffix was lost: %q", name)
	}
	if !strings.HasPrefix(name, dudeCredentialPrefix) {
		t.Fatalf("prefix missing: %q", name)
	}
}

func TestDudeCredentialHashedNameDiffersByCredentials(t *testing.T) {
	secure.SetKey("test-key")
	public := dudeCredentialGroup{
		Username: "admin",
		Password: "public",
		Devices:  []dudeCredentialDevice{{SourceID: "60", DeviceName: "RouterOS Edge"}},
	}
	private := public
	private.Password = "private"

	firstName, err := dudeCredentialHashedName(public)
	if err != nil {
		t.Fatal(err)
	}
	secondName, err := dudeCredentialHashedName(private)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := dudeCredentialHashedName(public)
	if err != nil {
		t.Fatal(err)
	}
	if firstName != repeated {
		t.Fatalf("hashed name is not stable for the same key and value: %q != %q", firstName, repeated)
	}
	if firstName == secondName {
		t.Fatalf("different credentials produced the same hashed name: %q", firstName)
	}
	if !strings.Contains(firstName, " #") || !strings.HasSuffix(firstName, " [60]") {
		t.Fatalf("unexpected hashed name: %q", firstName)
	}
	if len([]rune(firstName)) > dudeCredentialNameMax {
		t.Fatalf("hashed name too long: %d runes", len([]rune(firstName)))
	}
}

func TestDudeCredentialHashedNameReused(t *testing.T) {
	secure.SetKey("test-key")
	group := dudeCredentialGroup{
		Username: "admin",
		Password: "private",
		Devices:  []dudeCredentialDevice{{SourceID: "60", DeviceName: "RouterOS Edge"}},
	}
	base := dudeCredentialBaseName(group)
	hashed, err := dudeCredentialHashedName(group)
	if err != nil {
		t.Fatal(err)
	}
	existing := map[string]dudeRouterOSExistingCredential{
		strings.ToLower(base): {
			id: "conflict", name: base, username: "admin", password: "public", decoded: true,
		},
		strings.ToLower(hashed): {
			id: "profile-id", name: hashed, username: "admin", password: "private", decoded: true,
		},
	}
	name, profile, found, err := dudeCredentialTargetName(group, existing)
	if err != nil {
		t.Fatal(err)
	}
	if !found || name != hashed || profile.id != "profile-id" {
		t.Fatalf("hashed profile was not reused: name=%q found=%v profile=%+v", name, found, profile)
	}
	if !dudeRouterOSCredentialMatches(group, profile) {
		t.Fatal("reused profile should match the group credentials")
	}
}

func TestDudeCredentialBaseNameReused(t *testing.T) {
	group := dudeCredentialGroup{
		Username: "admin",
		Password: "secret",
		Devices:  []dudeCredentialDevice{{SourceID: "60", DeviceName: "RouterOS Edge"}},
	}
	base := dudeCredentialBaseName(group)
	existing := map[string]dudeRouterOSExistingCredential{
		strings.ToLower(base): {
			id: "profile-id", name: base, username: "admin", password: "secret", decoded: true,
		},
	}
	name, profile, found, err := dudeCredentialTargetName(group, existing)
	if err != nil {
		t.Fatal(err)
	}
	if !found || name != base || profile.id != "profile-id" {
		t.Fatalf("base profile was not reused: name=%q found=%v profile=%+v", name, found, profile)
	}
}

func TestDudeCredentialMayOverwrite(t *testing.T) {
	cases := []struct {
		name      string
		password  string
		overwrite bool
		want      bool
	}{
		{"empty without overwrite", "", false, false},
		{"empty with overwrite", "", true, false},
		{"password without overwrite", "secret", false, false},
		{"password with overwrite", "secret", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dudeCredentialMayOverwrite(tc.password, tc.overwrite); got != tc.want {
				t.Fatalf("dudeCredentialMayOverwrite(%q, %v) = %v, want %v", tc.password, tc.overwrite, got, tc.want)
			}
		})
	}
}

func TestDudeCredentialEnsureEmptyGroups(t *testing.T) {
	created, reused, assigned, skipped, warnings, err := ensureDudeRouterOSCredentials(nil, nil, nil, false, "dude.db")
	if err != nil || created != 0 || reused != 0 || assigned != 0 || skipped != 0 || len(warnings) != 0 {
		t.Fatalf("unexpected empty result: %d %d %d %d %v %v", created, reused, assigned, skipped, warnings, err)
	}
}
