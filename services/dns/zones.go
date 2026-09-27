package dns

import (
	"context"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/invoke"

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

// Resolvers is asked for the names a zone's file does not carry.
//
// IT TAKES THE QUESTION AND NOT A ZONE, for the reason Zones takes a query name:
// finding which zone covers a name is a walk up its labels, and whoever holds the
// index is the only party that can do it cheaply. The same party knows whether
// that zone named a resolver at all.
//
// A nil Resolution with a nil error means NOBODY WAS ASKED -- no zone here, or a
// zone with no resolver -- which is different from a resolver answering `absent`.
// The first falls through to whatever else this server can do; the second is a
// statement that the name does not exist.
type Resolvers interface {
	Resolve(ctx context.Context, q invoke.Question) (*invoke.Resolution, error)
}

// WithResolvers is where resolved answers come from. A server without one answers
// only what a zone wrote down and what it can route.
func WithResolvers(r Resolvers) Option {
	return func(o *options) error {
		if r == nil {
			return ErrNoResolvers
		}
		o.resolvers = r
		return nil
	}
}

// resolved asks the zone's resolver about one question.
//
// IT IS ASKED ONLY FOR WHAT THE RECORDS DID NOT ANSWER, which is the precedence
// the vocabulary states: a record an author can read in their own repository is
// not overridden by code. It is also the expensive arm -- a component
// instantiated per question -- so asking it first would put a wasm start on every
// query this server can already answer.
//
// AN ERROR IS NOT AN ANSWER. A resolver that failed has said nothing, so the
// query carries on to routing rather than becoming an absence somebody caches.
func (s *Service) resolved(ctx context.Context, qname string, qtype uint16) *invoke.Resolution {
	if s.opts.resolvers == nil {
		return nil
	}

	name := strings.ToLower(dns.Fqdn(qname))
	kind, ok := dns.TypeToString[qtype]
	if !ok {
		// A type this build has no name for cannot be put to a guest that
		// matches on the text form.
		return nil
	}

	answer, err := s.opts.resolvers.Resolve(ctx, invoke.Question{Name: name, Type: kind})
	if err != nil || answer == nil {
		return nil
	}
	return answer
}

// apexOf is the zone that covers a name, whatever it declares.
//
// SEPARATE FROM declared() BECAUSE A ZONE WITH NO LINES IS STILL A ZONE. declared
// bails when there are none -- it has nothing to parse -- but a resolver's answer
// needs the apex its lines are relative to, and that zone may carry no file at
// all. Costing one more Records call is the same trade the package doc already
// makes for authored/answersFor: whoever backs this caches the project, which is
// where the expense is.
func (s *Service) apexOf(ctx context.Context, qname string) string {
	if s.opts.zones == nil {
		return ""
	}
	apex, _, err := s.opts.zones.Records(ctx, strings.ToLower(strings.TrimSuffix(qname, ".")))
	if err != nil {
		return ""
	}
	return apex
}

// resolvedRecords parses what a resolver answered, through the SAME path the
// records arm uses -- one dns.NewRR per line under the zone's $ORIGIN.
//
// THE APEX COMES FROM THE ZONE AND NOT THE ANSWER, because a resolver answers in
// lines relative to the zone it belongs to and has no business naming a
// different one. A line that resolves to some other name is dropped, which is
// the same filter declared() applies to the file.
func (s *Service) resolvedRecords(apex, qname string, lines []string) []dns.RR {
	want := dns.Fqdn(qname)
	origin := "$ORIGIN " + dns.Fqdn(apex) + "\n$TTL " + strconv.FormatUint(uint64(s.ttlSeconds()), 10) + "\n"

	var out []dns.RR
	for _, line := range lines {
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
