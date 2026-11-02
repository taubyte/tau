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
	doc, ok := r.Doc("application", "api")
	require.True(t, ok)
	require.Equal(t, map[string]any{"name": "api_image"}, doc["package"])
	pkg, ok := r.Doc("package", "api_image")
	require.True(t, ok)
	require.Equal(t, map[string]any{"image": "acme/api:1"}, pkg["source"])

	// compose's own findings survive the delegation.
	require.Equal(t, importer.Blocked, finding(t, r, "restart").Level)
	require.Equal(t, importer.Blocked, finding(t, r, "healthcheck").Level)
}

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
