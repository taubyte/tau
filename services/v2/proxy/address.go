package proxy

import (
	"crypto/sha256"
	"fmt"
	"net/netip"
)

// address.go decides where a proxy answers.
//
// # An address per proxy, because at L4 the port is the address
//
// The kind declares neither, on purpose: a port number in the config would be
// the first authored value in this vocabulary with a GLOBAL collision domain —
// two projects both wanting 5432 on one edge address is not an error either
// project's schema can see. Give each proxy its own address and scarcity is
// per-address again, so the protocol's conventional port is free on it and
// `psql -h db.shop.example.com` connects with nothing looked up by hand.
//
// # Derived, not allocated
//
// An allocator is a table, and a table is a thing to replicate, to reconcile
// after a partition, and to leak entries into when a deploy is interrupted. A
// derivation is a pure function: every node computes the same address for the
// same resource without asking anyone, a redeploy of the same resource lands on
// the same address, and there is nothing to clean up when one goes away.
//
// That is only affordable because of where the bits come from. In a /64 there
// are 2^64 addresses and the operator is spending none of them by holding
// them — at a thousand proxies the chance of any two colliding is about 3 in
// 10^14. Deriving from a /112 would be a different conversation, which is why
// PrefixTooSmall exists rather than a silent modulo.
//
// # v4 is refused, and the vocabulary says why
//
// "The v4 address space is the real cost here, not anything in this schema. On
// v6 an address per proxy is free." A /24 holds 254 addresses and a derivation
// over it collides at about twenty proxies, so v4 needs a real allocator with
// real state — which is a different piece of work and not one to fake with a
// hash that will one day hand two tenants the same socket.

// MinPrefixBits is the largest prefix a derivation is allowed to work in.
//
// A /96 LEAVES 32 BITS, where a thousand proxies collide with probability about
// one in ten thousand — small, and not small enough to be silent about. Below
// that the birthday bound stops being a footnote.
const MinPrefixBits = 96

var (
	// ErrNotV6 is a v4 prefix. See the note above: v4 needs an allocator.
	ErrNotV6 = fmt.Errorf("%w: an address per proxy is derived, and only v6 has the room", ErrNoAddress)

	// ErrPrefixTooSmall is a prefix with too few bits to derive in.
	ErrPrefixTooSmall = fmt.Errorf("%w: too few bits to derive an address in", ErrNoAddress)

	// ErrNoAddress is the family of reasons a proxy has nowhere to answer.
	ErrNoAddress = fmt.Errorf("proxy: no address")
)

// AddressIn is where the proxy with this resource id answers, inside prefix.
//
// THE ID IS THE WHOLE INPUT, and deliberately not the name: a name can be
// reused after a resource is deleted, and an address that moved to a different
// workload because somebody reused a name would be the worst kind of surprise.
// An id is minted once and never again.
func AddressIn(prefix netip.Prefix, id string) (netip.Addr, error) {
	if !prefix.IsValid() || !prefix.Addr().Is6() || prefix.Addr().Is4In6() {
		return netip.Addr{}, fmt.Errorf("%w: %s", ErrNotV6, prefix)
	}
	if prefix.Bits() > MinPrefixBits {
		return netip.Addr{}, fmt.Errorf("%w: %s leaves %d", ErrPrefixTooSmall, prefix, 128-prefix.Bits())
	}
	if id == "" {
		return netip.Addr{}, fmt.Errorf("%w: a resource with no id has nothing to derive from", ErrNoAddress)
	}

	sum := sha256.Sum256([]byte(id))
	base := prefix.Masked().Addr().As16()

	// THE HOST BITS ONLY. Everything above the prefix length is the operator's
	// and is left exactly as it was — a derivation that wrote outside its
	// prefix would hand out addresses the operator does not route.
	var out [16]byte
	copy(out[:], base[:])
	for i := prefix.Bits(); i < 128; i++ {
		if sum[(i/8)%len(sum)]&(1<<uint(7-i%8)) != 0 {
			out[i/8] |= 1 << uint(7-i%8)
		}
	}

	addr := netip.AddrFrom16(out)
	// THE SUBNET-ROUTER ANYCAST ADDRESS IS NOT OURS to hand out (RFC 4291 §2.6.1),
	// and it is what an all-zero derivation would produce.
	if addr == prefix.Masked().Addr() {
		out[15] |= 1
		addr = netip.AddrFrom16(out)
	}
	return addr, nil
}
