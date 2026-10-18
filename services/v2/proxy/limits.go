package proxy

import (
	"net"
	"net/netip"

	"github.com/taubyte/tau/pkg/specs"
)

// limits.go is what one source may open, and how fast.
//
// # A rate and a count are two limits, and neither substitutes
//
// A gateway counts requests per minute because a request arrives, is answered
// and goes. A connection is HELD — a client pool, a subscriber, a psql session
// — so the count of open ones is the resource, and a proxy whose connection
// rate sits at zero can still be at its ceiling. The vocabulary asks for both
// and defaults neither, and this honours that: a limit nobody wrote is no
// limit, and one that was written is enforced on its own terms.
//
// # Refusal is a close, and it is silent
//
// A raw socket has no status line, so there is nowhere to put a 429 — the
// connection is closed with nothing written. It is also not logged, and that is
// deliberate rather than an omission: a line per refusal is a log amplifier
// pointed at this node by whoever is being refused, and hitting a limit the
// author wrote is expected behaviour rather than an incident.
//
// # The budget is per proxy, per source, per node
//
// Per proxy because the limit is written on the resource. Per source because
// that is what the vocabulary counts. Per node because a shared counter would
// put a round trip in front of every connection to enforce a limit whose
// purpose is to shed load — see pkg/ratelimit, which says the same thing from
// the other side.

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

// sourceKey is what this connection is counted under.
//
// `subnet` EXISTS BECAUSE ONE HOST CAN BE MANY ADDRESSES. A /64 is the smallest
// block anyone is routinely handed on v6 — a single machine's allocation — so
// counting per address there is counting per connection, and the limit is gone
// for exactly the caller it is meant to stop. /24 is the v4 equivalent that is
// small enough not to sweep up an unrelated network.
//
// AN UNKNOWN KEY COUNTS EVERYTHING TOGETHER rather than counting nothing: a
// misspelling that silently disabled the limit would look configured and not
// be, which is the failure worth avoiding.
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

// admit takes this connection's share of the proxy's budget, and hands back
// what to call when it is done.
//
// IT IS CHECKED BEFORE ANYTHING IS RESOLVED, so a refused connection costs a
// map lookup rather than a project read and a dial. That ordering is the
// difference between a limit that sheds load and one that is load.
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
