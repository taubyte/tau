//go:build linux

package netguard

import (
	"bytes"
	"net"
	"testing"
)

// Each denied range must encode as a boundary pair — start, then an exclusive
// end marker — with the correct byte width per family, so the nftables set
// matches the whole range. This validates rule construction without programming
// the kernel; that the encoding is the one the kernel accepts is the
// integration test's job.
func TestDeniedElementsIntervals(t *testing.T) {
	for _, fam := range []struct {
		v6    bool
		width int
	}{{false, net.IPv4len}, {true, net.IPv6len}} {
		els := deniedElements(fam.v6)
		if len(els) == 0 {
			t.Fatalf("v6=%v: expected some elements", fam.v6)
		}
		for _, e := range els {
			if len(e.Key) != fam.width {
				t.Errorf("v6=%v: element key width %d, want %d", fam.v6, len(e.Key), fam.width)
			}
			if len(e.KeyEnd) != 0 {
				t.Errorf("v6=%v: KeyEnd is set; the kernel rejects that form", fam.v6)
			}
		}
	}

	// 10.0.0.0/8 must be [10.0.0.0, 11.0.0.0).
	v4 := deniedElements(false)
	var found bool
	for i, e := range v4 {
		if !bytes.Equal(e.Key, net.IPv4(10, 0, 0, 0).To4()) || e.IntervalEnd {
			continue
		}
		if i+1 < len(v4) && v4[i+1].IntervalEnd &&
			bytes.Equal(v4[i+1].Key, net.IPv4(11, 0, 0, 0).To4()) {
			found = true
		}
	}
	if !found {
		t.Error("10.0.0.0/8 not encoded as start 10.0.0.0 + end marker 11.0.0.0")
	}

	// 224.0.0.0/4 and 240.0.0.0/4 are adjacent, so they merge into one range
	// that runs to the last address — which has no successor to mark an end
	// with. The last element must therefore be that open-ended start.
	if last := v4[len(v4)-1]; last.IntervalEnd ||
		!bytes.Equal(last.Key, net.IPv4(224, 0, 0, 0).To4()) {
		t.Errorf("last v4 element = %v (end=%v), want an open-ended 224.0.0.0 start",
			net.IP(last.Key), last.IntervalEnd)
	}
}

// Adjacent ranges must merge: ::/128 and ::1/128 would otherwise emit an end
// marker and a start element both keyed ::1, a duplicate the kernel rejects.
func TestDeniedRangesMergeAdjacent(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range deniedElements(true) {
		k := net.IP(e.Key).String()
		if seen[k] {
			t.Errorf("duplicate boundary %s", k)
		}
		seen[k] = true
	}
	first := deniedRanges(true)[0]
	if !first[0].Equal(net.IPv6zero) || !first[1].Equal(net.IPv6loopback) {
		t.Errorf("::/128 and ::1/128 not merged: first range = [%v, %v]", first[0], first[1])
	}
}

func TestLastAddr(t *testing.T) {
	_, n, _ := net.ParseCIDR("172.16.0.0/12")
	got := lastAddr(n.IP.To4(), n.Mask)
	if want := net.IPv4(172, 31, 255, 255).To4(); !bytes.Equal(got, want) {
		t.Errorf("lastAddr(172.16.0.0/12) = %v, want %v", got, want)
	}
}
