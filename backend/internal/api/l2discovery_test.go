package api

import "testing"

func TestMACHelpers(t *testing.T) {
	if got := normalizeMAC("AA:BB:CC:00:11:22"); got != "aabbcc001122" {
		t.Fatalf("normalizeMAC = %q", got)
	}
	if got := macFromOID("1.3.6.1.2.1.17.4.3.1.2.170.187.204.0.17.34"); got != "aabbcc001122" {
		t.Fatalf("macFromOID = %q", got)
	}
	if stpStateName("5") != "forwarding" || stpStateName("2") != "blocking" {
		t.Fatal("unexpected STP mapping")
	}
}

func TestResolveIfIndex(t *testing.T) {
	ifNames := map[string]string{"7": "ether7"}
	ifDescr := map[string]string{"9": "GigabitEthernet0/1"}
	if got := resolveIfIndex("ether7", ifNames, ifDescr); got != 7 {
		t.Fatalf("ifIndex by name = %d", got)
	}
	if got := resolveIfIndex("GigabitEthernet0/1", ifNames, ifDescr); got != 9 {
		t.Fatalf("ifIndex by descr = %d", got)
	}
	if got := resolveIfIndex("12", nil, nil); got != 12 {
		t.Fatalf("ifIndex numeric = %d", got)
	}
}
