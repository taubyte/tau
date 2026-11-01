package dns

import (
	"context"
	"errors"
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/stats"
)

// resolver_test is about the arm of a zone that is neither a file nor routing:
// a function asked, per question, for names nobody could write down in advance.

// asked is a resolver that answers from a table and records what it was asked.
type asked struct {
	answers  map[string]*invoke.Resolution
	err      error
	question []invoke.Question
}

func (a *asked) Resolve(_ context.Context, q invoke.Question) (*invoke.Resolution, error) {
	a.question = append(a.question, q)
	if a.err != nil {
		return nil, a.err
	}
	// A nil entry is "nobody was asked about this", which is what a zone with no
	// resolver looks like from here.
	return a.answers[q.Name], nil
}

func resolving(t *testing.T, apex string, r *asked, lines ...string) (*Service, *declared) {
	t.Helper()

	z := &declared{apex: apex, lines: lines}
	s, err := New(Config{NodeZone: "nodes.example.com"},
		WithRouter(&fakeRouter{nodes: map[string][]stats.Node{}}),
		WithZones(z), WithResolvers(r))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s, z
}

func records(lines ...string) *invoke.Resolution {
	return &invoke.Resolution{Exists: true, Records: lines}
}

func TestAResolverAnswersAName(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{answers: map[string]*invoke.Resolution{
		"geo.example.com.": records("geo 60 IN A 203.0.113.9"),
	}})

	reply := s.Answer(query("geo.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "203.0.113.9", reply.Answer[0].(*dns.A).A.String())
}

func TestAResolverIsAskedTheAbsoluteNameAndTheType(t *testing.T) {
	r := &asked{answers: map[string]*invoke.Resolution{}}
	s, _ := resolving(t, "example.com", r)

	s.Answer(query("Geo.Example.COM", dns.TypeAAAA))

	require.Len(t, r.question, 1)
	require.Equal(t, "geo.example.com.", r.question[0].Name,
		"absolute and lower-cased, so a guest matching on it need not guess")
	require.Equal(t, "AAAA", r.question[0].Type)
}

func TestAWrittenRecordBeatsTheResolver(t *testing.T) {
	r := &asked{answers: map[string]*invoke.Resolution{
		"www.example.com.": records("www 60 IN A 9.9.9.9"),
	}}
	s, _ := resolving(t, "example.com", r, "www 300 IN A 1.2.3.4")

	reply := s.Answer(query("www.example.com", dns.TypeA))
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "1.2.3.4", reply.Answer[0].(*dns.A).A.String(), "the authored line")
	require.Empty(t, r.question, "and the expensive arm was never asked")
}

func TestAResolverSayingEmptyIsNoErrorWithNoRecords(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{answers: map[string]*invoke.Resolution{
		"quiet.example.com.": {Exists: true, Records: []string{}},
	}})

	reply := s.Answer(query("quiet.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Empty(t, reply.Answer)
}

func TestAResolverSayingAbsentIsNXDomain(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{answers: map[string]*invoke.Resolution{
		"gone.example.com.": {Exists: false},
	}})

	reply := s.Answer(query("gone.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeNameError, reply.Rcode)
	require.Empty(t, reply.Answer)
}

func TestAbsentStillLetsRoutingAnswer(t *testing.T) {
	z := &declared{apex: "example.com"}
	router := &fakeRouter{nodes: map[string][]stats.Node{
		"shop.example.com": {{Peer: "node1", Ready: true, Addresses: []string{"203.0.113.5"}}},
	}}
	s, err := New(Config{NodeZone: "nodes.example.com"},
		WithRouter(router), WithZones(z),
		WithResolvers(&asked{answers: map[string]*invoke.Resolution{
			"shop.example.com.": {Exists: false},
		}}))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	reply := s.Answer(query("shop.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1, "the gateway still serves it")
}

func TestAFailingResolverIsNotAnAbsence(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{err: errors.New("the component would not start")})

	reply := s.Answer(query("anything.example.com", dns.TypeA))
	require.NotEqual(t, dns.RcodeNameError, reply.Rcode,
		"a resolver that could not run has not said the name is gone")
	require.Equal(t, dns.RcodeServerFailure, reply.Rcode)
}

func TestAZoneWithNoResolverIsUnchanged(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{answers: map[string]*invoke.Resolution{}},
		"www 300 IN A 1.2.3.4")

	require.Equal(t, dns.RcodeServerFailure, s.Answer(query("nope.example.com", dns.TypeA)).Rcode)

	reply := s.Answer(query("www.example.com", dns.TypeA))
	require.Len(t, reply.Answer, 1, "and the file still answers")
}

func TestAResolvedLineForAnotherNameIsNotAnAnswer(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{answers: map[string]*invoke.Resolution{
		"geo.example.com.": records("somewhereelse 60 IN A 203.0.113.9"),
	}})

	reply := s.Answer(query("geo.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Empty(t, reply.Answer)
}

func TestANilResolverIsRefused(t *testing.T) {
	_, err := New(Config{}, WithRouter(&fakeRouter{}), WithResolvers(nil))
	require.ErrorIs(t, err, ErrNoResolvers)
}

func TestAResolvedLineWithNoApexIsNotPlaced(t *testing.T) {
	answer := map[string]*invoke.Resolution{
		"geo.example.com.": records("geo 60 IN A 203.0.113.9"),
	}

	t.Run("no zone source at all", func(t *testing.T) {
		s, err := New(Config{NodeZone: "nodes.example.com"},
			WithRouter(&fakeRouter{nodes: map[string][]stats.Node{}}),
			WithResolvers(&asked{answers: answer}))
		require.NoError(t, err)
		t.Cleanup(func() { s.Close() })

		reply := s.Answer(query("geo.example.com", dns.TypeA))
		require.Equal(t, dns.RcodeSuccess, reply.Rcode)
		require.Empty(t, reply.Answer)
	})

	t.Run("the zone source failed", func(t *testing.T) {
		z := &declared{apex: "example.com", err: errors.New("register unreachable")}
		s, err := New(Config{NodeZone: "nodes.example.com"},
			WithRouter(&fakeRouter{nodes: map[string][]stats.Node{}}),
			WithZones(z), WithResolvers(&asked{answers: answer}))
		require.NoError(t, err)
		t.Cleanup(func() { s.Close() })

		reply := s.Answer(query("geo.example.com", dns.TypeA))
		require.Empty(t, reply.Answer, "an apex that could not be read is not an apex")
	})
}

func TestAnUnnamedQtypeIsNotAsked(t *testing.T) {
	r := &asked{answers: map[string]*invoke.Resolution{}}
	s, _ := resolving(t, "example.com", r)

	// 666 has no entry in miekg's TypeToString; 65535 does ("Reserved").
	s.Answer(query("geo.example.com", 666))
	require.Empty(t, r.question, "no text form, so nothing was asked")
}
