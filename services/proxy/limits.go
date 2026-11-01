package proxy

import (
	"net"
	"net/netip"

	"github.com/taubyte/tau/pkg/specs"
)

// A rate and a count are two limits: a connection is held, so a proxy whose
// connection rate is zero can still be at its ceiling. A limit nobody wrote is no
// limit.
//
// A refusal closes the connection with nothing written, and is not logged: a raw
// socket has no status line, and a line per refusal would be a log amplifier.
//
// The budget is per proxy, per source, per node; a shared counter would put a round
// trip before every connection a limit is meant to shed.

// limits is one proxy's rate_limit block.
type limits struct {
	rate       int
	burst      int
	concurrent int
	per        string
}

func limitsOf(r specs.Resource) limits {
	declared := specs.ReadProxy(r).RateLimit
	value := func(n *int64) int {
		if n == nil || *n < 0 {
			return 0
		}
		return int(*n)
	}
	return limits{
		rate:       value(declared.ConnectionsPerMinute),
		burst:      value(declared.Burst),
		concurrent: value(declared.Concurrent),
		per:        declared.Key,
	}
}

func (l limits) none() bool { return l.rate <= 0 && l.concurrent <= 0 }

// sourceKey is what this connection is counted under. `subnet` counts a /64 on v6,
// the smallest block a single machine is handed, and a /24 on v4. An unknown key
// counts everything together, so a misspelling cannot silently disable the limit.
func sourceKey(addr net.Addr, per string) string {
	if addr == nil {
		return ""
	}

	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return addr.String()
	}
	ip := ap.Addr().Unmap()

	if per != specs.ProxyRateLimitKeySubnet {
		return ip.String()
	}

	bits := 24
	if ip.Is6() {
		bits = 64
	}
	block, err := ip.Prefix(bits)
	if err != nil {
		return ip.String()
	}
	return block.String()
}

// admit takes this connection's share of the proxy's budget, and hands back what to
// call when it is done. It runs before anything is resolved, so a refusal costs a
// map lookup.
func (s *Service) admit(key string, l limits, from net.Addr) (func(), bool) {
	if l.none() {
		return func() {}, true
	}

	// The proxy's own key is part of the budget's: two proxies on one node
	// count separately, because the limit is written on the resource.
	who := key + "\x00" + sourceKey(from, l.per)

	if !s.buckets.Allow(who, l.rate, l.burst) {
		return nil, false
	}
	return s.open.Enter(who, l.concurrent)
}
