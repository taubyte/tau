package emit_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/importer"
	"github.com/taubyte/tau/pkg/importer/compose"
	"github.com/taubyte/tau/pkg/importer/emit"
)

const composeFile = `
services:
  api:
    image: ghcr.io/acme/api:1.4
    ports:
      - "8080:8080"
    environment:
      LOG_LEVEL: info
    deploy:
      resources:
        limits:
          memory: 512M
  db:
    image: postgres:16
    command: ["postgres", "-c", "max_connections=200"]
    volumes:
      - pgdata:/var/lib/postgresql/data
    deploy:
      resources:
        limits:
          memory: 2G
volumes:
  pgdata:
`

func imported(t *testing.T) importer.Result {
	t.Helper()
	r, err := compose.Import([]byte(composeFile))
	require.NoError(t, err)
	return r
}

func TestAComposeFileBecomesAProject(t *testing.T) {
	fs := afero.NewMemMapFs()

	report, err := emit.To(fs, "/project", "shop", imported(t))
	require.NoError(t, err)
	require.Equal(t, "shop", report.Project)

	// The layout is the DSL's, so a resource landed where an author would have
	// put it and the project has its own document.
	require.Contains(t, report.Written, "project.yaml")
	require.Contains(t, report.Written, "application/api.yaml")
	require.Contains(t, report.Written, "application/db.yaml")

	// No memory limit or volume size, so this does not compile yet; it must say so and
	// name every field.
	require.False(t, report.Compiles)
	require.NotEmpty(t, report.Why)
	require.NotEmpty(t, report.Missing)
}

func TestAProjectWithNothingMissingCompiles(t *testing.T) {
	fs := afero.NewMemMapFs()

	complete := importer.Result{Source: "hand"}
	complete.Set("application", "api", map[string]any{
		"source":    map[string]any{"image": "ghcr.io/acme/api:1.4"},
		"execution": map[string]any{"memory": "512MB"},
	})

	report, err := emit.To(fs, "/project", "shop", complete)
	require.NoError(t, err)
	require.Empty(t, report.Missing)
	require.True(t, report.Compiles, report.Why)

	compiles(t, fs, "/project")
}

func TestIdsAreMintedAndDistinct(t *testing.T) {
	fs := afero.NewMemMapFs()

	_, err := emit.To(fs, "/project", "shop", imported(t))
	require.NoError(t, err)

	ids := map[string]bool{}
	for _, path := range []string{"/project/project.yaml", "/project/application/api.yaml", "/project/application/db.yaml"} {
		b, err := afero.ReadFile(fs, path)
		require.NoError(t, err)

		id := idIn(t, string(b))
		require.NotEmpty(t, id, path)
		require.False(t, ids[id], "%s reused an id", path)
		ids[id] = true
	}
	require.Len(t, ids, 3)
}

func TestTheFindingsCarryThrough(t *testing.T) {
	fs := afero.NewMemMapFs()

	source := imported(t)
	report, err := emit.To(fs, "/project", "shop", source)
	require.NoError(t, err)

	require.Len(t, report.Findings, len(source.Findings))
	require.False(t, report.Compiles, "a compose import has holes; see TestAComposeFileBecomesAProject")
	require.NotEmpty(t, report.Blocked(), "this file overrides the image's command, which has nowhere to go")

	// And each level is reachable without the caller filtering by hand.
	for _, f := range report.Blocked() {
		require.Equal(t, importer.Blocked, f.Level)
	}
	for _, f := range report.Elsewhere() {
		require.Equal(t, importer.Elsewhere, f.Level)
	}
}

func TestSeveralResultsBecomeOneProject(t *testing.T) {
	fs := afero.NewMemMapFs()

	extra := importer.Result{Source: "hand"}
	extra.Set("application", "cache", map[string]any{
		"source":    map[string]any{"image": "redis:7"},
		"execution": map[string]any{"memory": "256MB"},
	})

	report, err := emit.To(fs, "/project", "shop", imported(t), extra)
	require.NoError(t, err)

	require.Contains(t, report.Written, "application/api.yaml")
	require.Contains(t, report.Written, "application/cache.yaml")
	require.Contains(t, report.Written, "volume/pgdata.yaml")
}

func TestWhatCouldNotBeFilledIsReported(t *testing.T) {
	fs := afero.NewMemMapFs()

	bare := importer.Result{Source: "hand"}
	bare.Set("application", "empty", map[string]any{})

	report, err := emit.To(fs, "/project", "shop", bare)
	require.NoError(t, err)

	require.NotEmpty(t, report.Missing, "an application with nothing in it is missing required fields")
	for _, m := range report.Missing {
		require.Equal(t, "application", m.Kind)
		require.Equal(t, "empty", m.Name)
		require.NotEmpty(t, m.Field)
		require.Contains(t, m.String(), "application/empty")
	}
}

func TestWhatItRefuses(t *testing.T) {
	_, err := emit.To(nil, "/project", "shop", imported(t))
	require.Error(t, err)

	_, err = emit.To(afero.NewMemMapFs(), "/project", "", imported(t))
	require.Error(t, err, "a project needs a name")

	// Resources with no kind: an importer that produced documents and did not
	// say what they are cannot be written anywhere sensible.
	nameless := importer.Result{Source: "hand"}
	nameless.Set("", "x", map[string]any{})
	_, err = emit.To(afero.NewMemMapFs(), "/project", "shop", nameless)
	require.Error(t, err)

	// A kind the vocabulary does not have.
	wrong := importer.Result{Source: "hand"}
	wrong.Set("widget", "x", map[string]any{})
	_, err = emit.To(afero.NewMemMapFs(), "/project", "shop", wrong)
	require.Error(t, err)

	// An empty result is a project with nothing in it, which is legal.
	report, err := emit.To(afero.NewMemMapFs(), "/project", "shop")
	require.NoError(t, err)
	require.Contains(t, report.Written, "project.yaml")
	require.Empty(t, report.Missing)
}

func TestTheReportIsStable(t *testing.T) {
	first, err := emit.To(afero.NewMemMapFs(), "/project", "shop", imported(t))
	require.NoError(t, err)

	second, err := emit.To(afero.NewMemMapFs(), "/project", "shop", imported(t))
	require.NoError(t, err)

	require.Equal(t, first.Written, second.Written)
	require.Equal(t, first.Missing, second.Missing)
	require.Equal(t, first.Deferred, second.Deferred)
	require.Equal(t, first.Findings, second.Findings)
	require.Equal(t, first.Compiles, second.Compiles)
	require.Equal(t, first.Why, second.Why)
}

func idIn(t *testing.T, doc string) string {
	t.Helper()
	for _, line := range strings.Split(doc, "\n") {
		if rest, ok := strings.CutPrefix(line, "id:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func TestAFilesystemThatWillNotTakeItSaysSo(t *testing.T) {
	_, err := emit.To(afero.NewReadOnlyFs(afero.NewMemMapFs()), "/project", "shop", imported(t))
	require.Error(t, err)
	require.Contains(t, err.Error(), "writing the project")
}

func TestElsewhereIsItsOwnList(t *testing.T) {
	fs := afero.NewMemMapFs()

	source := importer.Result{Source: "hand"}
	source.Add(importer.Finding{Key: "env", Level: importer.Elsewhere, Code: "SOURCE_IN_API:env",
		Detail: "environment variables live in the platform's database"})
	source.Add(importer.Finding{Key: "command", Level: importer.Blocked, Code: "NO_TAU_FEATURE:command",
		Detail: "no command field"})

	report, err := emit.To(fs, "/project", "shop", source)
	require.NoError(t, err)

	require.Len(t, report.Elsewhere(), 1)
	require.Equal(t, "env", report.Elsewhere()[0].Key)
	require.Len(t, report.Blocked(), 1)
	require.Equal(t, "command", report.Blocked()[0].Key)
}

func TestTheMissingListIsSorted(t *testing.T) {
	source := importer.Result{Source: "hand"}
	source.Set("application", "zebra", map[string]any{"source": map[string]any{"image": "x"}})
	source.Set("application", "alpha", map[string]any{"source": map[string]any{"image": "x"}})

	report, err := emit.To(afero.NewMemMapFs(), "/project", "shop", source)
	require.NoError(t, err)
	require.NotEmpty(t, report.Missing)

	last := ""
	for _, m := range report.Missing {
		key := m.Kind + "/" + m.Name + "/" + m.Field
		require.LessOrEqual(t, last, key)
		last = key
	}
	require.Contains(t, report.Missing[0].String(), "application/alpha")
}

func TestTheMissingListSortsAcrossKindsAndFields(t *testing.T) {
	source := importer.Result{Source: "hand"}
	source.Set("volume", "data", map[string]any{})
	source.Set("application", "api", map[string]any{})

	report, err := emit.To(afero.NewMemMapFs(), "/project", "shop", source)
	require.NoError(t, err)
	require.NotEmpty(t, report.Missing)

	// Kinds first, then names, then fields — and an application sorts before a
	// volume, which is the comparator being exercised rather than assumed.
	require.Equal(t, "application", report.Missing[0].Kind)
	require.Equal(t, "volume", report.Missing[len(report.Missing)-1].Kind)

	var applicationFields []string
	for _, m := range report.Missing {
		if m.Kind == "application" {
			applicationFields = append(applicationFields, m.Field)
		}
	}
	require.Equal(t, sortedCopy(applicationFields), applicationFields)
}

func TestADocumentTheSessionRefusesIsReported(t *testing.T) {
	source := importer.Result{Source: "hand"}
	source.Set("application", "api", map[string]any{
		// `name` is the resource's own identity and is not a block.
		"name": map[string]any{"nested": "wrong"},
	})

	_, err := emit.To(afero.NewMemMapFs(), "/project", "shop", source)
	if err != nil {
		require.Contains(t, err.Error(), "api")
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
