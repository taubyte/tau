package netguard

import (
	"net"
	"testing"
)

func TestIsDenied(t *testing.T) {
	denied := []string{
		"127.0.0.1", "127.1.2.3", // loopback
		"169.254.169.254", "169.254.0.1", // link-local / metadata
		"10.0.0.1", "10.255.255.255", // RFC1918
		"172.16.0.1", "172.31.255.255", // RFC1918 (edge of /12)
		"192.168.1.1",
		"100.64.0.1", "100.127.255.255", // CGNAT edges
		"0.0.0.0",
		"::1", "fe80::1", "fd00::abcd", // v6 loopback/link-local/ULA
		"::ffff:10.0.0.1", // IPv4-mapped private
		"64:ff9b::0a00:1", // NAT64 embedding 10.0.0.1
	}
	allowed := []string{
		"8.8.8.8", "1.1.1.1", "93.184.216.34",
		"172.32.0.1",                         // just past the RFC1918 /12
		"100.128.0.1",                        // just past CGNAT /10
		"2606:2800:220:1:248:1893:25c8:1946", // public v6
	}
	for _, s := range denied {
		if !IsDenied(net.ParseIP(s)) {
			t.Errorf("expected %s to be denied", s)
		}
	}
	for _, s := range allowed {
		if IsDenied(net.ParseIP(s)) {
			t.Errorf("expected %s to be allowed", s)
		}
	}
	if !IsDenied(nil) {
		t.Error("nil IP must be denied (fail closed)")
	}
}

func TestDialControl(t *testing.T) {
	if err := DialControl("tcp", "169.254.169.254:80", nil); err == nil {
		t.Error("metadata endpoint must be blocked")
	}
	if err := DialControl("tcp", "10.0.0.5:22", nil); err == nil {
		t.Error("RFC1918 must be blocked")
	}
	if err := DialControl("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("public address must be allowed, got %v", err)
	}
	if err := DialControl("tcp", "example.com:80", nil); err == nil {
		t.Error("unresolved hostname must be blocked (Control sees only resolved IPs)")
	}
}

func TestDeniedPrefixesIsACopy(t *testing.T) {
	p := DeniedPrefixes()
	if len(p) == 0 {
		t.Fatal("expected some denied prefixes")
	}
	p[0] = nil // mutating the returned slice must not corrupt the policy
	if IsDenied(net.ParseIP("127.0.0.1")) != true {
		t.Error("policy corrupted by mutating DeniedPrefixes result")
	}
}

// The two enforcement layers must agree. IsDenied is the guest layer and mixes
// stdlib predicates with the CIDR list; the firewall layer is built from the
// CIDR list alone, so anything IsDenied rejects that no prefix covers is a
// destination one layer blocks and the other permits. That is the drift the
// package doc promises cannot happen.
func TestNoDriftBetweenLayers(t *testing.T) {
	prefixes := DeniedPrefixes()
	covered := func(ip net.IP) bool {
		for _, n := range prefixes {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}

	for _, addr := range []string{
		"127.0.0.1", "0.0.0.0", "10.1.2.3", "172.20.0.1", "192.168.1.1",
		"169.254.169.254", "168.63.129.16", "100.64.0.1", "192.0.0.1",
		"198.18.0.1", "255.255.255.255", "224.0.0.251", "239.255.255.250",
		"240.0.0.1", "::1", "::", "fe80::1", "fc00::1", "64:ff9b::1",
		"2002::1", "2001::1", "ff02::fb", "ff01::1",
	} {
		ip := net.ParseIP(addr)
		if !IsDenied(ip) {
			t.Errorf("%s: IsDenied says permitted", addr)
			continue
		}
		if !covered(ip) {
			t.Errorf("%s: denied by IsDenied but covered by no prefix, so the firewall layer permits it", addr)
		}
	}
}
