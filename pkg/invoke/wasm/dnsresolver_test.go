package wasm_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// dnsresolver_test drives `taubyte:dns/dns-resolver` against a REAL component,
// for the reason every other world's test gives: a double would agree with
// whatever this package assumed about the variant it lifts.
//
// WHAT IS ACTUALLY AT STAKE HERE IS THE THREE ARMS. Every other deciding world
// in this tree answers yes or no; DNS has a third answer, and the two that look
// alike are the ones that must not be confused. `empty` says the name is here
// and holds nothing of this type, which stops a client looking elsewhere.
// `absent` is NXDOMAIN, which is cacheable AS AN ABSENCE — so lifting one as the
// other does not produce a wrong reply, it produces a wrong reply that every
// resolver on the path remembers.

// warren is the fixture: it answers `geo` with records, `quiet` with empty, and
// everything else absent — all three arms, from one component.
func warren(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("fixtures/warren.wasm")
	require.NoError(t, err, "run `cargo component build --release` in fixtures/warren")
	return code
}

func warrenSpec() invoke.Spec {
	return invoke.Spec{
		Name:     "warren",
		Artifact: "bafywarren",
		Settings: map[string]string{wasm.SettingImplements: wasm.WorldDNSResolver},
	}
}

// TestARESOLVERANSWERSINZONEFILENOTATION, which is what lets both arms of a
// zone converge on one parse path: the line below is the same shape the file arm
// holds, so services/dns reads it with the same dns.NewRR.
func TestADNSResolverAnswersWithZoneFileLines(t *testing.T) {
	r := tier(t, warren(t))

	got, err := r.Resolve(t.Context(), warrenSpec(), invoke.Question{Name: "geo.example.com", Type: "A"})
	require.NoError(t, err)
	require.True(t, got.Exists, "a name it answered records for exists")
	require.Equal(t, []string{"geo 60 IN A 203.0.113.9"}, got.Records)
}

// TestTHETYPEREACHESTHEGUEST. The question carries the type as text so a
// resolver can answer per-type, and warren echoes it into the record — which is
// the only way to show the field arrived rather than assume it.
func TestADNSResolverIsToldWhichTypeWasAsked(t *testing.T) {
	r := tier(t, warren(t))

	got, err := r.Resolve(t.Context(), warrenSpec(), invoke.Question{Name: "geo.example.com", Type: "AAAA"})
	require.NoError(t, err)
	require.Equal(t, []string{"geo 60 IN AAAA 2001:db8::9"}, got.Records,
		"the guest BRANCHED on the type it was asked -- an AAAA holds an IPv6 address, "+
			"and a single template that spliced the type in would emit a line no parser accepts")
}

// TestEMPTYISNOTABSENCE. The name is in the zone and holds nothing of this
// type: NOERROR with no records. Read as absence it would become NXDOMAIN and
// be cached as one.
func TestADNSResolverSaysANameExistsWithNothingOfThatType(t *testing.T) {
	r := tier(t, warren(t))

	got, err := r.Resolve(t.Context(), warrenSpec(), invoke.Question{Name: "quiet.example.com", Type: "A"})
	require.NoError(t, err)
	require.True(t, got.Exists, "`empty` means the name IS here")
	require.Empty(t, got.Records, "and holds nothing of the type asked for")
}

// TestABSENCEISABSENCE — the other side of the pair.
func TestADNSResolverSaysNoSuchName(t *testing.T) {
	r := tier(t, warren(t))

	got, err := r.Resolve(t.Context(), warrenSpec(), invoke.Question{Name: "nothing.example.com", Type: "A"})
	require.NoError(t, err)
	require.False(t, got.Exists, "`absent` is NXDOMAIN")
	require.Empty(t, got.Records)
}

// TestTheTwoEmptyAnswersAreDistinguishable states the property the two tests
// above only imply separately: same empty record list, different existence.
// Collapsing Resolution to a []string would make this assertion unwritable.
func TestEmptyAndAbsentAreNotTheSameAnswer(t *testing.T) {
	r := tier(t, warren(t))

	empty, err := r.Resolve(t.Context(), warrenSpec(), invoke.Question{Name: "quiet.example.com", Type: "A"})
	require.NoError(t, err)
	absent, err := r.Resolve(t.Context(), warrenSpec(), invoke.Question{Name: "nothing.example.com", Type: "A"})
	require.NoError(t, err)

	require.Empty(t, empty.Records)
	require.Empty(t, absent.Records)
	require.NotEqual(t, empty.Exists, absent.Exists,
		"both answered no records, and only one of them said the name exists")
}

// TestAHANDLERISNOTARESOLVER. A component built against another world must be
// refused by name rather than instantiated and asked: whatever it returned would
// be lifted as a DNS answer.
func TestResolveRefusesAHandler(t *testing.T) {
	r := tier(t, handler(t))

	_, err := r.Resolve(t.Context(), invoke.Spec{Name: "checkout", Artifact: "bafy"},
		invoke.Question{Name: "geo.example.com", Type: "A"})
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.Contains(t, err.Error(), "dns-resolver", "and which world was wanted")
}
