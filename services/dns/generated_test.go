package dns

import (
	"context"
	"errors"
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/stats"
)

// fakeHosts is a host index: published names, or an error for every name.
type fakeHosts struct {
	published map[string]bool
	err       error
}

func (h fakeHosts) ResolveHost(_ context.Context, fqdn string) (string, string, error) {
	if h.err != nil {
		return "", "", h.err
	}
	if h.published[fqdn] {
		return "shop", "prod", nil
	}
	return "", "", pkgregister.ErrNoHost
}

func generatedService(t *testing.T, r Router, h Hosts) *Service {
	t.Helper()
	s, err := New(Config{}, WithRouter(r), WithGenerated(Generated{Zone: "Tau.Example.io.", Hosts: h, Service: "gateway"}))
	require.NoError(t, err)
	return s
}

var gateways = serving("gateway",
	stats.Node{Peer: "g1", Ready: true, Addresses: []string{"203.0.113.1", "2001:db8::1"}},
	stats.Node{Peer: "g2", Ready: true, Addresses: []string{"203.0.113.2"}},
)

func TestAPublishedGeneratedNameIsAnsweredWithEveryGateway(t *testing.T) {
	s := generatedService(t, gateways, fakeHosts{published: map[string]bool{"api.p1.tau.example.io": true}})

	reply := s.Answer(query("API.p1.tau.example.io", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 2)
	require.Equal(t, "203.0.113.1", reply.Answer[0].(*dns.A).A.String())
	require.Equal(t, uint32(DefaultTTL.Seconds()), reply.Answer[0].Header().Ttl)

	reply = s.Answer(query("api.p1.tau.example.io", dns.TypeAAAA))
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "2001:db8::1", reply.Answer[0].(*dns.AAAA).AAAA.String())

	// Another type at a published name exists and holds nothing.
	reply = s.Answer(query("api.p1.tau.example.io", dns.TypeMX))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Empty(t, reply.Answer)
}

func TestAGeneratedNameNobodyPublishedIsNXDOMAIN(t *testing.T) {
	s := generatedService(t, gateways, fakeHosts{published: map[string]bool{"api.p1.tau.example.io": true}})
	reply := s.Answer(query("typo.p1.tau.example.io", dns.TypeA))
	require.Equal(t, dns.RcodeNameError, reply.Rcode)
	require.Empty(t, reply.Answer)
}

func TestAnUnpublishedProjectLabelIsNeverNXDOMAIN(t *testing.T) {
	s := generatedService(t, gateways, fakeHosts{published: map[string]bool{"api.p1.tau.example.io": true}})
	reply := s.Answer(query("p1.tau.example.io", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Empty(t, reply.Answer)

	// Published at the label itself, it is answered.
	s = generatedService(t, gateways, fakeHosts{published: map[string]bool{"p1.tau.example.io": true}})
	require.Len(t, s.Answer(query("p1.tau.example.io", dns.TypeA)).Answer, 2)
}

func TestAPublishedNameWithNoGatewayIsSERVFAIL(t *testing.T) {
	s := generatedService(t, &fakeRouter{nodes: map[string][]stats.Node{}}, fakeHosts{published: map[string]bool{"api.p1.tau.example.io": true}})
	require.Equal(t, dns.RcodeServerFailure, s.Answer(query("api.p1.tau.example.io", dns.TypeA)).Rcode)
}

func TestARegisterThatCannotBeAskedIsSERVFAIL(t *testing.T) {
	s := generatedService(t, gateways, fakeHosts{err: errors.New("unavailable")})
	require.Equal(t, dns.RcodeServerFailure, s.Answer(query("api.p1.tau.example.io", dns.TypeA)).Rcode)
}

func TestNamesOutsideTheCloudZoneAreUntouched(t *testing.T) {
	r := serving("shop.example.com", stats.Node{Peer: "w", Ready: true, Addresses: []string{"198.51.100.1"}})
	r.nodes["gateway"] = gateways.nodes["gateway"]
	s := generatedService(t, r, fakeHosts{err: errors.New("never asked")})

	require.Len(t, s.Answer(query("shop.example.com", dns.TypeA)).Answer, 1)
	// The apex is the operator's, not a generated name.
	require.Equal(t, dns.RcodeServerFailure, s.Answer(query("tau.example.io", dns.TypeA)).Rcode)
	require.Equal(t, dns.RcodeServerFailure, s.Answer(query("xtau.example.io", dns.TypeA)).Rcode)
}

func TestCAAStillAnswersUnderTheCloudZone(t *testing.T) {
	s, err := New(Config{CAA: []CAA{{Zone: "tau.example.io", Issuer: "letsencrypt.org"}}},
		WithRouter(gateways), WithGenerated(Generated{Zone: "tau.example.io", Hosts: fakeHosts{}, Service: "gateway"}))
	require.NoError(t, err)
	reply := s.Answer(query("typo.p1.tau.example.io", dns.TypeCAA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1)
}

func TestAGeneratedZoneNeedsAllThreeParts(t *testing.T) {
	for _, g := range []Generated{
		{Hosts: fakeHosts{}, Service: "gateway"},
		{Zone: "tau.example.io", Service: "gateway"},
		{Zone: "tau.example.io", Hosts: fakeHosts{}},
	} {
		_, err := New(Config{}, WithRouter(gateways), WithGenerated(g))
		require.ErrorIs(t, err, ErrBadGenerated)
	}
}
