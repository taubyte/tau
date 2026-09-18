package dns

import (
	"context"
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/stats"
)

// zones_test is about the half of this server that is not routing: a static
// assertion an author wrote, in zone-file notation, about names they own.

// declared is a zone source with one zone in it.
type declared struct {
	apex  string
	lines []string
	err   error
	asked []string
}

func (d *declared) Records(_ context.Context, name string) (string, []string, error) {
	d.asked = append(d.asked, name)
	if d.err != nil {
		return "", nil, d.err
	}
	return d.apex, d.lines, nil
}

func zoning(t *testing.T, apex string, lines ...string) (*Service, *declared) {
	t.Helper()

	z := &declared{apex: apex, lines: lines}
	s, err := New(Config{NodeZone: "nodes.example.com"},
		WithRouter(&fakeRouter{nodes: map[string][]stats.Node{}}),
		WithZones(z))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s, z
}

// TestATYPETHISSERVERCANNOTROUTE is the whole point: no amount of stats
// produces an MX, and a project that moved its domain here and lost its mail
// has not moved its domain.
func TestAZoneAnswersTypesRoutingCannot(t *testing.T) {
	s, _ := zoning(t, "example.com",
		"@ 3600 IN MX 10 mail.example.com.",
		`@ 3600 IN TXT "v=spf1 -all"`,
		"www 300 IN A 1.2.3.4",
		"docs 300 IN CNAME pages.example.net.",
	)

	mx := s.Answer(query("example.com", dns.TypeMX))
	require.Equal(t, dns.RcodeSuccess, mx.Rcode)
	require.Len(t, mx.Answer, 1)
	require.Equal(t, "mail.example.com.", mx.Answer[0].(*dns.MX).Mx)
	require.EqualValues(t, 10, mx.Answer[0].(*dns.MX).Preference)

	txt := s.Answer(query("example.com", dns.TypeTXT))
	require.Len(t, txt.Answer, 1)
	require.Equal(t, []string{"v=spf1 -all"}, txt.Answer[0].(*dns.TXT).Txt)

	cname := s.Answer(query("docs.example.com", dns.TypeCNAME))
	require.Len(t, cname.Answer, 1)
	require.Equal(t, "pages.example.net.", cname.Answer[0].(*dns.CNAME).Target)
}

// TestARELATIVENAMEISRELATIVETOTHEAPEX, which is what a zone file means and
// what an author writes.
func TestARelativeRecordResolvesUnderItsApex(t *testing.T) {
	s, _ := zoning(t, "example.com", "www 300 IN A 1.2.3.4")

	reply := s.Answer(query("www.example.com", dns.TypeA))
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "1.2.3.4", reply.Answer[0].(*dns.A).A.String())
	require.Equal(t, "www.example.com.", reply.Answer[0].Header().Name)

	// And the apex itself is a different name.
	require.Empty(t, s.Answer(query("example.com", dns.TypeA)).Answer)
}

// TestANAUTHOREDRECORDWINS over anything that could be routed. It is the more
// specific statement: somebody wrote it down, about that exact name.
func TestAnAuthoredRecordBeatsARoutedOne(t *testing.T) {
	z := &declared{apex: "example.com", lines: []string{"shop 300 IN A 1.2.3.4"}}

	s, err := New(Config{NodeZone: "nodes.example.com"},
		WithRouter(srvService("shop.example.com",
			stats.Node{Peer: peerNamed(t, "n"), Ready: true, Addresses: []string{"203.0.113.9"}})),
		WithZones(z))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	reply := s.Answer(query("shop.example.com", dns.TypeA))
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "1.2.3.4", reply.Answer[0].(*dns.A).A.String(),
		"the authored record, not the node serving the name")
}

// TestANAMEWITHNORECORDOFTHATTYPEEXISTS. NOERROR with no records says "no A
// here"; SERVFAIL would say this server is broken and send the resolver away.
func TestANameDeclaredAtAnotherTypeIsNoerrorAndEmpty(t *testing.T) {
	s, _ := zoning(t, "example.com", "@ 3600 IN MX 10 mail.example.com.")

	reply := s.Answer(query("example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode, "the name exists and has no A")
	require.Empty(t, reply.Answer)
}

// TestANAMENOZONEDECLARESISSTILLSERVFAIL, because nothing is serving it.
func TestANameNoZoneDeclaresFallsThrough(t *testing.T) {
	s, _ := zoning(t, "example.com", "www 300 IN A 1.2.3.4")

	require.Equal(t, dns.RcodeServerFailure, s.Answer(query("other.example.com", dns.TypeA)).Rcode)
}

// TestONETYPODOESNOTTAKEAZONEOFFTHEAIR. The author is the only party who can
// fix either, and one record missing is better than all of them.
func TestAnUnparseableLineIsSkippedAndTheRestAreServed(t *testing.T) {
	s, _ := zoning(t, "example.com",
		"this is not a resource record at all",
		"www 300 IN A 1.2.3.4",
	)

	reply := s.Answer(query("www.example.com", dns.TypeA))
	require.Len(t, reply.Answer, 1, "the good line still answers")
}

// TestASOURCETHATFAILEDISNOTANANSWER, and the query falls through to routing
// rather than being served as if the zone were empty.
func TestAZoneSourceThatErrorsFallsThrough(t *testing.T) {
	s, z := zoning(t, "example.com", "www 300 IN A 1.2.3.4")
	z.err = context.DeadlineExceeded

	require.Equal(t, dns.RcodeServerFailure, s.Answer(query("www.example.com", dns.TypeA)).Rcode)
}

// TestNOZONESOURCEATALL answers exactly what it could before, which is what
// makes this additive.
func TestAServerWithNoZoneSourceStillRoutes(t *testing.T) {
	s := newService(t, srvService("shop.example.com",
		stats.Node{Peer: peerNamed(t, "n"), Ready: true, Addresses: []string{"203.0.113.9"}}), withZone)

	reply := s.Answer(query("shop.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "203.0.113.9", reply.Answer[0].(*dns.A).A.String())
}

func TestANilZoneSourceIsRefusedAtConstruction(t *testing.T) {
	_, err := New(Config{}, WithRouter(&fakeRouter{}), WithZones(nil))
	require.ErrorIs(t, err, ErrNoZones)
}
