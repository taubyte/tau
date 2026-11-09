package dns

import (
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/stats"
)

func TestTheCloudsZoneAnswersCAAAtEveryName(t *testing.T) {
	s := newService(t, &fakeRouter{nodes: map[string][]stats.Node{}}, func(c *Config) {
		c.CAA = []CAA{{Zone: "G.Example.com.", Issuer: "letsencrypt.org"}}
	})
	for _, name := range []string{"g.example.com", "p.g.example.com", "a.p.g.example.com"} {
		reply := s.Answer(query(name, dns.TypeCAA))
		require.Equal(t, dns.RcodeSuccess, reply.Rcode, name)
		require.Len(t, reply.Answer, 1, name)
		caa := reply.Answer[0].(*dns.CAA)
		require.Equal(t, "issue", caa.Tag)
		require.Equal(t, "letsencrypt.org", caa.Value)
	}

	// Outside the zone nothing changes: nothing serves it, so SERVFAIL.
	require.Equal(t, dns.RcodeServerFailure, s.Answer(query("xg.example.com", dns.TypeCAA)).Rcode)
}

func TestACAARecordNeedsAZoneAndAnIssuer(t *testing.T) {
	for _, caa := range []CAA{{Zone: "g.example.com"}, {Issuer: "letsencrypt.org"}} {
		_, err := build(Config{CAA: []CAA{caa}}, withRouter(&fakeRouter{}))
		require.ErrorContains(t, err, "caa record")
	}
}
