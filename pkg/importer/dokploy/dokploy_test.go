package dokploy

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

func has(r importer.Result, key string) bool {
	for _, f := range r.Findings {
		if f.Key == key {
			return true
		}
	}
	return false
}

func TestTheAppDefinitionIsTheComposeFile(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: acme/api:1
    volumes:
      - data:/var/lib/data
`)
	require.Equal(t, Source, r.Source)
	require.Equal(t, "acme/api:1", r.Resources["api"]["image"])
	require.Equal(t, importer.Blocked, finding(t, r, "volumes").Level,
		"compose's own findings survive the delegation")
}

// TestInheritedVariablesAreNamed. A compose file full of ${{project.KEY}} is not
// self-contained: the values are a level up, and importing the file alone gives
// the references with nothing behind them.
func TestInheritedVariablesAreNamed(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: acme/api:1
    environment:
      DATABASE_URL: "${{project.DATABASE_URL}}"
      REGION: "${{ environment.REGION }}"
`)
	f := finding(t, r, "inherited_variables")
	require.Equal(t, importer.Elsewhere, f.Level)
	require.Contains(t, f.Detail, "project.DATABASE_URL")
	require.Contains(t, f.Detail, "environment.REGION",
		"whitespace inside the braces is Dokploy-legal and must still match")
}

// TestAFileWithNoInheritanceSaysNothingAboutIt. Reporting it unconditionally
// would train people to skip the findings.
func TestAFileWithNoInheritanceSaysNothingAboutIt(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: acme/api:1
    environment:
      PLAIN: value
`)
	require.False(t, has(r, "inherited_variables"))
}

// TestComposesOwnInterpolationIsNotMistakenForDokploys. ${VAR} is compose's and
// is reported through that path; only ${{...}} is a level-up reference.
func TestComposesOwnInterpolationIsNotMistakenForDokploys(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: "acme/api:${TAG}"
`)
	require.False(t, has(r, "inherited_variables"))
}

// TestTheFileOverlayIsReportedUnconditionally. A patch writes files into the
// cloned repo before the build, so the compose file and the repository can both
// look complete and the built image be neither. Nothing reveals one, so the
// only way to know is to ask.
func TestTheFileOverlayIsReportedUnconditionally(t *testing.T) {
	r := importOf(t, "services:\n  api:\n    image: x\n")

	f := finding(t, r, "patch")
	require.Equal(t, importer.Elsewhere, f.Level)
	require.Equal(t, "SOURCE_OUT_OF_BAND:file-overlay", f.Code)
	require.Contains(t, f.Detail, "ask")
}

func TestTheApiHeldValuesAreNamed(t *testing.T) {
	r := importOf(t, "services:\n  api:\n    image: x\n")

	require.Contains(t, finding(t, r, "env").Detail, "x-api-key")
	require.Equal(t, importer.Elsewhere, finding(t, r, "domains").Level)
	require.Equal(t, importer.Elsewhere, finding(t, r, "registry").Level)
}

func TestEveryElsewhereFindingNamesNoContract(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: "${{project.IMAGE}}"
`)
	for _, f := range r.Elsewhere() {
		require.Empty(t, f.Needs, "%s", f.Key)
	}
}

func TestAMalformedComposeIsRefused(t *testing.T) {
	_, err := Import([]byte("services: [not a mapping"))
	require.Error(t, err)
}
