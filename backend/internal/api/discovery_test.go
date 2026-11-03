package api

import "testing"

func TestParseDiscoveryCIDRs(t *testing.T) {
	networks, total, err := parseDiscoveryCIDRs("10.0.0.0/24, 192.168.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(networks) != 2 || total != 512 {
		t.Fatalf("networks=%d total=%d", len(networks), total)
	}
}

func TestParseDiscoveryCIDRsDeduplicates(t *testing.T) {
	networks, total, err := parseDiscoveryCIDRs("10.0.0.0/24\n10.0.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(networks) != 1 || total != 256 {
		t.Fatalf("networks=%d total=%d", len(networks), total)
	}
}

func TestParseDiscoveryCIDRsAllowsSixteenBit(t *testing.T) {
	_, total, err := parseDiscoveryCIDRs("192.168.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	if total != maxDiscoveryHosts {
		t.Fatalf("total=%d", total)
	}
}

func TestParseDiscoveryCIDRsRejectsLargeRange(t *testing.T) {
	if _, _, err := parseDiscoveryCIDRs("10.0.0.0/8"); err == nil {
		t.Fatal("expected /8 to exceed the limit")
	}
}

func TestParseDiscoveryCIDRsRejectsIPv6(t *testing.T) {
	if _, _, err := parseDiscoveryCIDRs("fd00::/64"); err == nil {
		t.Fatal("expected IPv6 to be rejected")
	}
}
