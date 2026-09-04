package coolify

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/importer"
)

func importOf(t *testing.T, yml string) importer.Result {
	t.Helper()
	r, err := Import([]byte(yml))
	require.NoError(t, err)
	return r
}

func finding(t *testing.T, r importer.Result, key string) importer.Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("no finding for %q", key)
	return importer.Finding{}
}

// TestTheAppDefinitionIsTheComposeFile, so every fidelity finding compose makes
// is made here too — a second parser would drift from the first.
func TestTheAppDefinitionIsTheComposeFile(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: acme/api:1
    restart: always
    healthcheck:
      test: ["CMD", "true"]
`)
	require.Equal(t, Source, r.Source)
	require.Equal(t, "acme/api:1", r.Resources["api"]["image"])

	// compose's own findings survive the delegation.
	require.Equal(t, importer.Blocked, finding(t, r, "restart").Level)
	require.Equal(t, importer.Blocked, finding(t, r, "healthcheck").Level)
}

// TestTheReadOnlyTokenTrapIsNamedUpFront. A Coolify `read` token returns
// REDACTED env values rather than an error, so an importer that accepted it
// would produce a project whose every secret is the empty string.
func TestTheReadOnlyTokenTrapIsNamedUpFront(t *testing.T) {
	r := importOf(t, "services:\n  api:\n    image: x\n")

	f := finding(t, r, "env")
	require.Equal(t, importer.Elsewhere, f.Level)
	require.Contains(t, f.Detail, "read:sensitive")
	require.Contains(t, f.Detail, "REDACTED",
		"the trap is that it does not error; that has to be in the words somebody reads")
	require.Empty(t, f.Needs, "there is nothing for tau to build here")

	require.Equal(t, []string{"read", "read:sensitive"}, RequiredScopes)
}

// TestServicePasswordsMustBeReadNotRegenerated. Coolify generates them once and
// the running service is already using them; minting new ones produces a
// service whose credentials no longer match its own data.
func TestServicePasswordsMustBeReadNotRegenerated(t *testing.T) {
	r := importOf(t, "services:\n  db:\n    image: postgres\n")

	f := finding(t, r, "service_password")
	require.Equal(t, importer.Elsewhere, f.Level)
	require.Contains(t, f.Detail, "never regenerated")
}

func TestDomainsAreElsewhere(t *testing.T) {
	r := importOf(t, "services:\n  api:\n    image: x\n")
	require.Equal(t, importer.Elsewhere, finding(t, r, "domains").Level)
}

// TestTheStructureMapsOneToOne — the rare thing that carries perfectly, and
// worth saying so the reader knows which parts are easy.
func TestTheStructureMapsOneToOne(t *testing.T) {
	r := importOf(t, "services:\n  api:\n    image: x\n")
	f := finding(t, r, "environments")
	require.Equal(t, importer.Approximated, f.Level)
	require.Contains(t, f.Detail, "1:1")
}

func TestEveryElsewhereFindingNamesNoContract(t *testing.T) {
	r := importOf(t, "services:\n  api:\n    image: x\n")
	for _, f := range r.Elsewhere() {
		require.Empty(t, f.Needs,
			"%s names a contract, but connecting an account is not something for tau to build", f.Key)
	}
}

func TestAMalformedComposeIsRefused(t *testing.T) {
	_, err := Import([]byte("services: [not a mapping"))
	require.Error(t, err)
}
