package dns

import (
	"context"
	"errors"
	"strings"

	"github.com/miekg/dns"
)

// challenges.go answers DNS-01: the TXT records of `_acme-challenge.<name>`
// for a name in the cloud's zone, while an order is proving it.
//
// They are asked for at query time from the reader the node composes — the
// cm service's challenge cache — rather than carried in the discovery
// view: a CA looks a handful of times per order, and the values already live
// in a store with a lifetime of their own.
//
// Before every other answer for the name, because `_acme-challenge` sits where
// a generated name's labels do and would otherwise be routed as one. A
// reader that cannot answer is SERVFAIL, for the package's first
// refusal: a CA reading "no record" fails the order, "try again" does not.
// So is a reader holding no value: a CA asks only while an order proves the
// name, and its value not being here yet is not its absence.

// Challenges reads the DNS-01 values being presented for a name.
type Challenges interface {
	Challenges(ctx context.Context, fqdn string) ([]string, error)
}

// ErrBadChallenges is a challenge source missing its zone or its reader.
var ErrBadChallenges = errors.New("dns: DNS-01 answers need the cloud's zone and a reader of them")

const challengeLabel = "_acme-challenge."

type challenges struct {
	zone   string
	reader Challenges
}

// WithChallenges answers `_acme-challenge.<name>` TXT for names in zone.
func WithChallenges(zone string, reader Challenges) Option {
	return func(o *options) error {
		zone = strings.ToLower(strings.Trim(zone, "."))
		if zone == "" || reader == nil {
			return ErrBadChallenges
		}
		o.challenges = &challenges{zone: zone, reader: reader}
		return nil
	}
}

// answerChallenge answers a DNS-01 name, or reports false when name is not one.
func (s *Service) answerChallenge(ctx context.Context, reply *dns.Msg, name string, q dns.Question) bool {
	c := s.opts.challenges
	if c == nil {
		return false
	}
	fqdn, ok := strings.CutPrefix(name, challengeLabel)
	if !ok || (fqdn != c.zone && !strings.HasSuffix(fqdn, "."+c.zone)) {
		return false
	}
	if q.Qtype != dns.TypeTXT {
		return true
	}
	values, err := c.reader.Challenges(ctx, fqdn)
	if err != nil || len(values) == 0 {
		reply.Rcode = dns.RcodeServerFailure
		return true
	}
	for _, v := range values {
		reply.Answer = append(reply.Answer, &dns.TXT{
			// NO TTL: the set changes with every order, and a resolver
			// holding the set an earlier order was proved with fails the
			// next one proving the same name.
			Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0},
			Txt: []string{v},
		})
	}
	return true
}
