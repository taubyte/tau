// Package netguard defines the single network-egress policy for untrusted code
// running on a tau node: which destinations must be blocked so a tenant WASM
// function or a build container cannot reach node-local services or cloud
// infrastructure (loopback, private networks, and the link-local metadata
// endpoint), while still reaching the public internet.
//
// The same policy feeds two enforcement layers so they can never drift:
//   - a net.Dialer control guard for the in-process WASM guest's HTTP client
//     (pkg/vm-low-orbit/http) — portable Go, every OS;
//   - an nftables ruleset for build containers (pkg/containers firewall) — Linux
//     only, built from DeniedPrefixes.
package netguard

import (
	"fmt"
	"net"
	"syscall"
	"time"
)

// deniedCIDRs are the networks untrusted code must not reach. Public internet is
// everything not listed here.
var deniedCIDRs = mustParseCIDRs(
	// IPv4
	"0.0.0.0/8",          // "this host on this network"; 0.0.0.0 often routes to loopback
	"127.0.0.0/8",        // loopback
	"10.0.0.0/8",         // RFC1918 private
	"172.16.0.0/12",      // RFC1918 private
	"192.168.0.0/16",     // RFC1918 private
	"169.254.0.0/16",     // link-local, incl. cloud metadata 169.254.169.254
	"100.64.0.0/10",      // CGNAT (RFC6598)
	"192.0.0.0/24",       // IETF protocol assignments
	"198.18.0.0/15",      // benchmarking
	"255.255.255.255/32", // limited broadcast
	// IPv6
	"::1/128",      // loopback
	"::/128",       // unspecified
	"fe80::/10",    // link-local
	"fc00::/7",     // unique local (ULA)
	"64:ff9b::/96", // NAT64 well-known prefix (embeds IPv4)
	"2002::/16",    // 6to4 (embeds IPv4, incl. private)
	"2001::/32",    // Teredo (embeds IPv4)
	// IPv4-mapped IPv6 (::ffff:a.b.c.d) is handled by To4() normalisation in
	// IsDenied, not a CIDR here: a "::ffff:0:0/96" entry collapses to /0 against
	// a 4-byte IP and would deny every IPv4 destination.
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("netguard: invalid CIDR " + c + ": " + err.Error())
		}
		out = append(out, n)
	}
	return out
}

// IsDenied reports whether untrusted code may not connect to ip. It fails
// closed: a nil or unparseable address is denied.
func IsDenied(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4 // normalise IPv4-mapped IPv6 so it matches the IPv4 ranges
	}
	// stdlib flag checks catch families/cases a static CIDR list can miss.
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, n := range deniedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// DialControl is a net.Dialer.Control function that rejects a connection whose
// resolved address is denied. Control runs after DNS resolution, on the concrete
// address being dialled, so it defeats DNS-rebinding — the guard sees the real
// IP, not the hostname.
func DialControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("netguard: refusing connection to unresolved address %q", address)
	}
	if IsDenied(ip) {
		return fmt.Errorf("netguard: destination %s is not permitted", ip)
	}
	return nil
}

// RestrictedDialer returns a net.Dialer that enforces the egress policy and the
// given connect timeout. Use its DialContext for an untrusted http.Transport.
func RestrictedDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: DialControl}
}

// DeniedPrefixes returns a copy of the denied networks, for building the
// nftables sets in the container firewall. Callers split by address family via
// (*net.IPNet).IP.To4().
func DeniedPrefixes() []*net.IPNet {
	out := make([]*net.IPNet, len(deniedCIDRs))
	copy(out, deniedCIDRs)
	return out
}
