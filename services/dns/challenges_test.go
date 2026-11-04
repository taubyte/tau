package dns

import (
	"context"
	"errors"
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

type presented struct {
	values map[string][]string
	err    error
}

func (p presented) Challenges(_ context.Context, fqdn string) ([]string, error) {
	return p.values[fqdn], p.err
}

func TestADNS01ChallengeIsAnsweredFromTheCertificateNodes(t *testing.T) {
	p := presented{values: map[string][]string{
		"build.tau.example.io": {"one", "two"},
		"tau.example.io":       {"apex"},
	}}
	s := servicesService(t, builders, WithChallenges("tau.example.io.", p))

	reply := s.Answer(query("_acme-challenge.Build.tau.example.io", dns.TypeTXT))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 2)
	require.Equal(t, []string{"one"}, reply.Answer[0].(*dns.TXT).Txt)

	// The zone's own, which is where a wildcard is proved.
	reply = s.Answer(query("_acme-challenge.tau.example.io", dns.TypeTXT))
	require.Equal(t, []string{"apex"}, reply.Answer[0].(*dns.TXT).Txt)

	// Nothing presented is SERVFAIL, not "no record": the value may not have
	// reached this node yet. Another type holds nothing.
	reply = s.Answer(query("_acme-challenge.other.tau.example.io", dns.TypeTXT))
	require.Equal(t, dns.RcodeServerFailure, reply.Rcode)
	require.Empty(t, reply.Answer)
	reply = s.Answer(query("_acme-challenge.build.tau.example.io", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Empty(t, reply.Answer)

	// Outside the zone it is not this function's name.
	reply = s.Answer(query("_acme-challenge.example.com", dns.TypeTXT))
	require.Empty(t, reply.Answer)

	// A reader that cannot answer is SERVFAIL, never "no record".
	down := servicesService(t, builders, WithChallenges("tau.example.io", presented{err: errors.New("down")}))
	require.Equal(t, dns.RcodeServerFailure, down.Answer(query("_acme-challenge.tau.example.io", dns.TypeTXT)).Rcode)

	_, err := New(Config{}, WithRouter(builders), WithChallenges("", p))
	require.ErrorIs(t, err, ErrBadChallenges)
	_, err = New(Config{}, WithRouter(builders), WithChallenges("tau.example.io", nil))
	require.ErrorIs(t, err, ErrBadChallenges)
}
