package wasm_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// Against a real component, since `empty` (NOERROR) and `absent` (NXDOMAIN,
// cacheable) must not be confused.

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

func TestADNSResolverAnswersWithZoneFileLines(t *testing.T) {
	r := tier(t, warren(t))

	got, err := r.Resolve(t.Context(), warrenSpec(), invoke.Question{Name: "geo.example.com", Type: "A"})
	require.NoError(t, err)
	require.True(t, got.Exists, "a name it answered records for exists")
	require.Equal(t, []string{"geo 60 IN A 203.0.113.9"}, got.Records)
}

func TestADNSResolverIsToldWhichTypeWasAsked(t *testing.T) {
	r := tier(t, warren(t))

	got, err := r.Resolve(t.Context(), warrenSpec(), invoke.Question{Name: "geo.example.com", Type: "AAAA"})
	require.NoError(t, err)
	require.Equal(t, []string{"geo 60 IN AAAA 2001:db8::9"}, got.Records,
		"the guest BRANCHED on the type it was asked -- an AAAA holds an IPv6 address, "+
			"and a single template that spliced the type in would emit a line no parser accepts")
}

func TestADNSResolverSaysANameExistsWithNothingOfThatType(t *testing.T) {
	r := tier(t, warren(t))

	got, err := r.Resolve(t.Context(), warrenSpec(), invoke.Question{Name: "quiet.example.com", Type: "A"})
	require.NoError(t, err)
	require.True(t, got.Exists, "`empty` means the name IS here")
	require.Empty(t, got.Records, "and holds nothing of the type asked for")
}

func TestADNSResolverSaysNoSuchName(t *testing.T) {
	r := tier(t, warren(t))

	got, err := r.Resolve(t.Context(), warrenSpec(), invoke.Question{Name: "nothing.example.com", Type: "A"})
	require.NoError(t, err)
	require.False(t, got.Exists, "`absent` is NXDOMAIN")
	require.Empty(t, got.Records)
}

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

func TestResolveRefusesAHandler(t *testing.T) {
	r := tier(t, handler(t))

	_, err := r.Resolve(t.Context(), invoke.Spec{Name: "checkout", Artifact: "bafy"},
		invoke.Question{Name: "geo.example.com", Type: "A"})
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.Contains(t, err.Error(), "dns-resolver", "and which world was wanted")
}
