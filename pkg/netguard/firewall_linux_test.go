//go:build linux

package netguard

import (
	"bytes"
	"net"
	"testing"
)

// Each denied CIDR must encode as one interval element [network, broadcast]
// (Key/KeyEnd) with the correct byte width per family, so the nftables set
// matches the whole range. This validates rule construction without programming
// the kernel.
func TestDeniedElementsIntervals(t *testing.T) {
	v4 := deniedElements(false)
	if len(v4) == 0 {
		t.Fatal("expected some v4 elements")
	}
	for _, e := range v4 {
		if len(e.Key) != net.IPv4len || len(e.KeyEnd) != net.IPv4len {
			t.Errorf("v4 element widths key=%d keyEnd=%d, want 4/4", len(e.Key), len(e.KeyEnd))
		}
	}

	// 10.0.0.0/8 must be [10.0.0.0, 10.255.255.255].
	var found bool
	for _, e := range v4 {
		if bytes.Equal(e.Key, net.IPv4(10, 0, 0, 0).To4()) &&
			bytes.Equal(e.KeyEnd, net.IPv4(10, 255, 255, 255).To4()) {
			found = true
		}
	}
	if !found {
		t.Error("10.0.0.0/8 not encoded as [10.0.0.0, 10.255.255.255]")
	}

	v6 := deniedElements(true)
	if len(v6) == 0 {
		t.Fatal("expected some v6 elements")
	}
	for _, e := range v6 {
		if len(e.Key) != net.IPv6len || len(e.KeyEnd) != net.IPv6len {
			t.Errorf("v6 element widths key=%d keyEnd=%d, want 16/16", len(e.Key), len(e.KeyEnd))
		}
	}
}

func TestLastAddr(t *testing.T) {
	_, n, _ := net.ParseCIDR("172.16.0.0/12")
	got := lastAddr(n.IP.To4(), n.Mask)
	if want := net.IPv4(172, 31, 255, 255).To4(); !bytes.Equal(got, want) {
		t.Errorf("lastAddr(172.16.0.0/12) = %v, want %v", got, want)
	}
}
