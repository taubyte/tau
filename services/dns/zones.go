package dns

import (
	"context"
	"strconv"
	"strings"

	"github.com/miekg/dns"
)

// zones.go answers from the records an author wrote.
//
// # The half of this service that is not routing
//
// Everything else here answers from live measurements: who is serving this
// name, how loaded are they, which port did their proxy get. A `zone` resource
// is the opposite — it is a static assertion, in zone-file notation, about
// names this project owns. `www 300 IN A 1.2.3.4` is not a routing decision and
// no amount of stats will produce it.
//
// It is also the only way this cloud answers a type it cannot route: MX, TXT,
// CNAME, NS, CAA. A project that moves its domain here and loses its mail
// records has not moved its domain.
//
// # An authored record wins
//
// Where both could answer — an A record for a name a gateway also serves — the
// authored one is served. It is the more specific statement: somebody wrote it
// down, about that exact name, on purpose.
//
// # It is parsed per query, and per line
//
// A zone is a handful of lines and parsing them costs less than the cache
// invalidation would: a deploy changes the records, and a parsed copy held
// behind a TTL would answer with the old ones for as long as that TTL says.
// Whoever backs this is free to cache the PROJECT, which is where the expense
// actually is.
//
// PER LINE, because a zone parser stops at the first syntax error and loses
// every good line after it — so one typo would take a whole zone off the air.

// Zones is what a project declares for a name it owns.
//
// IT TAKES THE QUERY NAME AND NOT AN APEX, because finding which zone covers a
// name is a walk up its labels and whoever holds the index is the only party
// that can do it cheaply. The answer is the apex the lines are relative to,
// which a zone file cannot be parsed without.
type Zones interface {
	Records(ctx context.Context, name string) (apex string, lines []string, err error)
}

// WithZones is where authored records come from. A server without one answers
// only what it can route.
func WithZones(z Zones) Option {
	return func(o *options) error {
		if z == nil {
			return ErrNoZones
		}
		o.zones = z
		return nil
	}
}

// authored is every record a project declares that matches this question.
func (s *Service) authored(ctx context.Context, qname string, qtype uint16) []dns.RR {
	var out []dns.RR
	for _, rr := range s.declared(ctx, qname) {
		if rr.Header().Rrtype == qtype {
			out = append(out, rr)
		}
	}
	return out
}

// answersFor says whether a project declares anything at all at this name,
// whatever the type.
//
// IT IS WHAT SEPARATES NOERROR FROM SERVFAIL. A name a zone declares only an MX
// for, asked for an A, exists and has no A — which is NOERROR with no records.
// Answering SERVFAIL there would tell a resolver this server is broken, and it
// would go and ask somebody else.
func (s *Service) answersFor(ctx context.Context, qname string) bool {
	return len(s.declared(ctx, qname)) > 0
}

// declared is every record an author wrote at this exact name.
func (s *Service) declared(ctx context.Context, qname string) []dns.RR {
	if s.opts.zones == nil {
		return nil
	}

	name := strings.ToLower(strings.TrimSuffix(qname, "."))
	apex, lines, err := s.opts.zones.Records(ctx, name)
	if err != nil || apex == "" || len(lines) == 0 {
		return nil
	}

	want := dns.Fqdn(qname)
	origin := "$ORIGIN " + dns.Fqdn(apex) + "\n$TTL " + strconv.FormatUint(uint64(s.ttlSeconds()), 10) + "\n"

	var out []dns.RR
	for _, line := range lines {
		// ONE LINE AT A TIME, AND THAT IS THE WHOLE REASON THIS IS NOT A ZONE
		// PARSER. A zone parser stops at the first syntax error and every good
		// line after it is lost — so one typo would take a zone off the air,
		// which is worse than one record missing. The author is the only party
		// who can fix either.
		rr, parseErr := dns.NewRR(origin + line)
		if parseErr != nil || rr == nil {
			continue
		}
		if strings.EqualFold(rr.Header().Name, want) {
			out = append(out, rr)
		}
	}
	return out
}
