package api

import (
	"testing"

	"github.com/netmonitor/netmonitor/internal/dude"
)

func TestDudeLinkMonitoring(t *testing.T) {
	const deviceUUID = "11111111-1111-4111-8111-111111111111"
	deviceIDs := map[string]string{"device-1": deviceUUID}
	sentinel := ^uint32(0)

	tests := []struct {
		name          string
		link          dude.Link
		enabled       bool
		wantMastering string
		wantDeviceID  string
		wantIfIndex   int
	}{
		{
			name:          "disabled keeps link simple",
			link:          dude.Link{MasterDevice: "device-1", MasterInterface: 7},
			enabled:       false,
			wantMastering: "simple",
		},
		{
			name:          "no interface keeps link simple",
			link:          dude.Link{MasterDevice: "device-1"},
			enabled:       true,
			wantMastering: "simple",
		},
		{
			name:          "no master device keeps link simple",
			link:          dude.Link{MasterInterface: 7},
			enabled:       true,
			wantMastering: "simple",
		},
		{
			name:          "master device not imported keeps link simple",
			link:          dude.Link{MasterDevice: "device-missing", MasterInterface: 7},
			enabled:       true,
			wantMastering: "simple",
		},
		{
			name:          "imported master device and interface enable snmp",
			link:          dude.Link{MasterDevice: "device-1", MasterInterface: 7},
			enabled:       true,
			wantMastering: "snmp",
			wantDeviceID:  deviceUUID,
			wantIfIndex:   7,
		},
		{
			name:          "sentinel interface keeps link simple",
			link:          dude.Link{MasterDevice: "device-1", MasterInterface: int(sentinel)},
			enabled:       true,
			wantMastering: "simple",
		},
		{
			name:          "negative interface keeps link simple",
			link:          dude.Link{MasterDevice: "device-1", MasterInterface: -1},
			enabled:       true,
			wantMastering: "simple",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mastering, deviceID, ifIndex := dudeLinkMonitoring(tt.link, deviceIDs, tt.enabled)
			if mastering != tt.wantMastering {
				t.Fatalf("mastering = %q, want %q", mastering, tt.wantMastering)
			}
			if deviceID != tt.wantDeviceID {
				t.Fatalf("deviceID = %q, want %q", deviceID, tt.wantDeviceID)
			}
			if ifIndex != tt.wantIfIndex {
				t.Fatalf("ifIndex = %d, want %d", ifIndex, tt.wantIfIndex)
			}
		})
	}
}

func TestDudeLinkHasMasterInterface(t *testing.T) {
	sentinel := ^uint32(0)
	tests := []struct {
		name string
		link dude.Link
		want bool
	}{
		{name: "zero", link: dude.Link{}, want: false},
		{name: "positive", link: dude.Link{MasterInterface: 7}, want: true},
		{name: "negative", link: dude.Link{MasterInterface: -1}, want: false},
		{name: "sentinel", link: dude.Link{MasterInterface: int(sentinel)}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dudeLinkHasMasterInterface(tt.link); got != tt.want {
				t.Fatalf("dudeLinkHasMasterInterface(%+v) = %v, want %v", tt.link, got, tt.want)
			}
		})
	}
}

func TestDudeLinkMonitoringWithSNMPProfile(t *testing.T) {
	const (
		deviceWithProfile    = "11111111-1111-4111-8111-111111111111"
		deviceWithoutProfile = "22222222-2222-4222-8222-222222222222"
	)
	deviceIDs := map[string]string{
		"device-with-profile":    deviceWithProfile,
		"device-without-profile": deviceWithoutProfile,
	}
	snmpDeviceIDs := map[string]bool{deviceWithProfile: true}

	tests := []struct {
		name           string
		link           dude.Link
		enabled        bool
		wantMastering  string
		wantDeviceID   string
		wantIfIndex    int
		wantDowngraded bool
	}{
		{
			name:          "snmp master with profile is monitored",
			link:          dude.Link{MasterDevice: "device-with-profile", MasterInterface: 7},
			enabled:       true,
			wantMastering: "snmp",
			wantDeviceID:  deviceWithProfile,
			wantIfIndex:   7,
		},
		{
			name:           "snmp master without profile is downgraded",
			link:           dude.Link{MasterDevice: "device-without-profile", MasterInterface: 8},
			enabled:        true,
			wantMastering:  "simple",
			wantDowngraded: true,
		},
		{
			name:          "disabled link is not downgraded",
			link:          dude.Link{MasterDevice: "device-with-profile", MasterInterface: 7},
			enabled:       false,
			wantMastering: "simple",
		},
		{
			name:          "link without interface is not downgraded",
			link:          dude.Link{MasterDevice: "device-with-profile"},
			enabled:       true,
			wantMastering: "simple",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mastering, deviceID, ifIndex, downgraded := dudeLinkMonitoringWithSNMPProfile(tt.link, deviceIDs, snmpDeviceIDs, tt.enabled)
			if mastering != tt.wantMastering {
				t.Fatalf("mastering = %q, want %q", mastering, tt.wantMastering)
			}
			if deviceID != tt.wantDeviceID {
				t.Fatalf("deviceID = %q, want %q", deviceID, tt.wantDeviceID)
			}
			if ifIndex != tt.wantIfIndex {
				t.Fatalf("ifIndex = %d, want %d", ifIndex, tt.wantIfIndex)
			}
			if downgraded != tt.wantDowngraded {
				t.Fatalf("downgraded = %v, want %v", downgraded, tt.wantDowngraded)
			}
		})
	}
}
