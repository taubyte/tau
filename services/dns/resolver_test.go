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

// TestARESOLVERANSWERSANAMENOFILECOULDCARRY -- one label per tenant, which is
// the case the whole arm exists for.
func TestAResolverAnswersAName(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{answers: map[string]*invoke.Resolution{
		"geo.example.com.": records("geo 60 IN A 203.0.113.9"),
	}})

	reply := s.Answer(query("geo.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "203.0.113.9", reply.Answer[0].(*dns.A).A.String())
}

// TestTheQuestionCarriesTheTypeAsText, which is what lets a resolver answer
// per-type and is the only thing it is told besides the name.
func TestAResolverIsAskedTheAbsoluteNameAndTheType(t *testing.T) {
	r := &asked{answers: map[string]*invoke.Resolution{}}
	s, _ := resolving(t, "example.com", r)

	s.Answer(query("Geo.Example.COM", dns.TypeAAAA))

	require.Len(t, r.question, 1)
	require.Equal(t, "geo.example.com.", r.question[0].Name,
		"absolute and lower-cased, so a guest matching on it need not guess")
	require.Equal(t, "AAAA", r.question[0].Type)
}

// TestTHEFILEWINS. The vocabulary says a record an author can read in their own
// repository is not overridden by code, and this is that precedence: the
// resolver is not even asked for a name the lines already answer.
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

// TestEMPTYSTOPSTHECLIENTLOOKINGELSEWHERE: the name is in the zone and holds
// nothing of this type. NOERROR with no records, never NXDOMAIN.
func TestAResolverSayingEmptyIsNoErrorWithNoRecords(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{answers: map[string]*invoke.Resolution{
		"quiet.example.com.": {Exists: true, Records: []string{}},
	}})

	reply := s.Answer(query("quiet.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Empty(t, reply.Answer)
}

// TestABSENTISTHEONLYTHINGTHATEARNSNXDOMAIN. This server answers SERVFAIL for
// "nothing is serving it", because that is not "it does not exist". A zone's own
// resolver saying no IS a statement about existence, and it is the one input
// here that is.
func TestAResolverSayingAbsentIsNXDomain(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{answers: map[string]*invoke.Resolution{
		"gone.example.com.": {Exists: false},
	}})

	reply := s.Answer(query("gone.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeNameError, reply.Rcode)
	require.Empty(t, reply.Answer)
}

// TestABSENTDOESNOTUNSERVEAROUTEDNAME. A project may serve shop.example.com from
// a gateway while declaring example.com as a zone, so a resolver that does not
// know a name must not take it off the air.
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

// TestARESOLVERTHATFAILEDHASSAIDNOTHING -- and must not become an absence
// anybody caches. It falls through to whatever else this server can do.
func TestAFailingResolverIsNotAnAbsence(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{err: errors.New("the component would not start")})

	reply := s.Answer(query("anything.example.com", dns.TypeA))
	require.NotEqual(t, dns.RcodeNameError, reply.Rcode,
		"a resolver that could not run has not said the name is gone")
	require.Equal(t, dns.RcodeServerFailure, reply.Rcode)
}

// TestNOTHINGWASASKED is a zone with no resolver, which is every zone in the
// corpus today: a nil answer with a nil error changes nothing about the query.
func TestAZoneWithNoResolverIsUnchanged(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{answers: map[string]*invoke.Resolution{}},
		"www 300 IN A 1.2.3.4")

	require.Equal(t, dns.RcodeServerFailure, s.Answer(query("nope.example.com", dns.TypeA)).Rcode)

	reply := s.Answer(query("www.example.com", dns.TypeA))
	require.Len(t, reply.Answer, 1, "and the file still answers")
}

// TestALINEABOUTSOMEOTHERNAMEISDROPPED, the same filter the file arm applies:
// a resolver answers about the name it was asked, and a line that resolves
// elsewhere is not an answer to this question.
func TestAResolvedLineForAnotherNameIsNotAnAnswer(t *testing.T) {
	s, _ := resolving(t, "example.com", &asked{answers: map[string]*invoke.Resolution{
		"geo.example.com.": records("somewhereelse 60 IN A 203.0.113.9"),
	}})

	reply := s.Answer(query("geo.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Empty(t, reply.Answer)
}

// TestANilResolverIsRefused -- WithZones' reasoning: a server told to ask
// something must be given something to ask.
func TestANilResolverIsRefused(t *testing.T) {
	_, err := New(Config{}, WithRouter(&fakeRouter{}), WithResolvers(nil))
	require.ErrorIs(t, err, ErrNoResolvers)
}
