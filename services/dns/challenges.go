package dns

import (
	"context"
	"errors"
	"strings"

	"github.com/miekg/dns"
)

// DNS-01: the TXT records of `_acme-challenge.<name>` for a name in the cloud's
// zone, read at query time from the cm service's challenge cache.
//
// It is answered before everything else for the name, since `_acme-challenge` sits
// where a generated name's labels do. A reader that cannot answer, or holds no value
// yet, is SERVFAIL: "no record" would fail the order.

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
