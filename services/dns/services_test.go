package dns

import (
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/stats"
)

var builders = serving("build",
	stats.Node{Peer: "b1", Ready: true, Addresses: []string{"203.0.113.7", "2001:db8::7"}},
)

func servicesService(t *testing.T, r Router, opts ...Option) *Service {
	t.Helper()
	opts = append([]Option{WithRouter(r), WithServices(Services{Zone: "Tau.Example.io.", Names: []string{"build", "auth"}})}, opts...)
	s, err := New(Config{}, opts...)
	require.NoError(t, err)
	return s
}

func TestAServiceIsAnsweredWithTheNodesRunningIt(t *testing.T) {
	s := servicesService(t, builders)

	reply := s.Answer(query("Build.tau.example.io", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "203.0.113.7", reply.Answer[0].(*dns.A).A.String())

	reply = s.Answer(query("build.tau.example.io", dns.TypeAAAA))
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "2001:db8::7", reply.Answer[0].(*dns.AAAA).AAAA.String())

	// Another type at a service's name exists and holds nothing.
	reply = s.Answer(query("build.tau.example.io", dns.TypeMX))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Empty(t, reply.Answer)
}

func TestAKnownServiceNobodyRunsIsSERVFAIL(t *testing.T) {
	s := servicesService(t, builders)
	require.Equal(t, dns.RcodeServerFailure, s.Answer(query("auth.tau.example.io", dns.TypeA)).Rcode)
}

func TestAnUnknownServiceIsNXDOMAIN(t *testing.T) {
	s := servicesService(t, builders)
	reply := s.Answer(query("patrick.tau.example.io", dns.TypeA))
	require.Equal(t, dns.RcodeNameError, reply.Rcode)
	require.Empty(t, reply.Answer)
}

func TestAnUnknownLabelBesideGeneratedNamesIsAProjectLabel(t *testing.T) {
	r := serving("build", builders.nodes["build"]...)
	r.nodes["gateway"] = gateways.nodes["gateway"]
	s := servicesService(t, r, WithGenerated(Generated{Zone: "tau.example.io", Hosts: fakeHosts{published: map[string]bool{"api.p1.tau.example.io": true}}, Service: "gateway"}))

	// The service still wins its name...
	require.Equal(t, "203.0.113.7", s.Answer(query("build.tau.example.io", dns.TypeA)).Answer[0].(*dns.A).A.String())
	// ...and a label that is none is an empty non-terminal, never NXDOMAIN.
	reply := s.Answer(query("p1.tau.example.io", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Empty(t, reply.Answer)
	// Generated names below are answered as before.
	require.Len(t, s.Answer(query("api.p1.tau.example.io", dns.TypeA)).Answer, 2)
}

func TestNamesThatAreNotOneLabelUnderTheZoneAreNotServices(t *testing.T) {
	r := serving("build", builders.nodes["build"]...)
	s := servicesService(t, r)
	// The apex, a name two labels down, and a name outside are routed as any
	// other name, which nothing reports.
	for _, name := range []string{"tau.example.io", "x.build.tau.example.io", "build.example.io"} {
		require.Equal(t, dns.RcodeServerFailure, s.Answer(query(name, dns.TypeA)).Rcode, name)
	}
}

func TestAServicesZoneNeedsBothParts(t *testing.T) {
	for _, svc := range []Services{{Names: []string{"auth"}}, {Zone: "tau.example.io"}} {
		_, err := New(Config{}, WithRouter(builders), WithServices(svc))
		require.ErrorIs(t, err, ErrBadServices)
	}
}
