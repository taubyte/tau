package emit_test

import (
	"sort"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/importer"
	"github.com/taubyte/tau/pkg/importer/compose"
	"github.com/taubyte/tau/pkg/importer/coolify"
	"github.com/taubyte/tau/pkg/importer/dokploy"
	"github.com/taubyte/tau/pkg/importer/emit"
	"github.com/taubyte/tau/pkg/importer/fly"
	"github.com/taubyte/tau/pkg/importer/vercel"
	"github.com/taubyte/tau/pkg/invoke/container"
	"github.com/taubyte/tau/pkg/invoke/js"
	"github.com/taubyte/tau/pkg/invoke/static"
	"github.com/taubyte/tau/pkg/invoke/wasm"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// An imported project must be runnable, not merely compile: every emitted kind must
// declare an engine this repository has a tier for.

// implemented is every engine this repository has a tier for, by the name the
// vocabulary uses. A tier added here without a kind, or a kind added there
// without a tier, is what the test below is about.
var implemented = map[string]bool{
	container.Name: true,
	js.Name:        true,
	static.Name:    true,
	wasm.Name:      true,
}

// absent is every engine the vocabulary names that no tier here runs YET, and why.
// An entry is a gap with a reason, not a waiver: the test below fails the day its
// tier lands and the entry is still here, so the list cannot outlive what it records.
var absent = map[string]string{}

// theFlyFile, theVercelOutput and the compose file above are one representative
// descriptor each — enough to make every importer emit its kinds.
const theFlyFile = `
app = "shop"
primary_region = "iad"

[build]
  image = "ghcr.io/acme/shop:1.4"

[[services]]
  internal_port = 8080
  protocol = "tcp"

[[vm]]
  memory = "512mb"
  cpus = 1
`

const theVercelOutput = `{
  "buildCommand": "next build",
  "installCommand": "pnpm i",
  "outputDirectory": ".next",
  "functions": {
    "api/orders.ts": { "memory": 512, "maxDuration": 30 }
  }
}`

// emitted is every (kind, name) one importer produced.
func emitted(t *testing.T, results ...importer.Result) map[string][]string {
	t.Helper()

	fs := afero.NewMemMapFs()
	report, err := emit.To(fs, "/project", "shop", results...)
	require.NoError(t, err)

	byKind := map[string][]string{}
	for _, r := range results {
		for _, res := range r.Resources {
			byKind[res.Kind] = append(byKind[res.Kind], res.Name)
		}
	}
	require.NotEmpty(t, byKind, "an importer that emits nothing imports nothing")

	// It is also written, which is what the rest of this package proves; the
	// report is read here only so a failure to write is not silent.
	require.NotEmpty(t, report.Written)
	return byKind
}

func TestEverythingAnImporterEmitsCanBeRun(t *testing.T) {
	fly, err := fly.Import([]byte(theFlyFile))
	require.NoError(t, err)

	vercelResult, err := vercel.Import([]byte(theVercelOutput))
	require.NoError(t, err)

	coolifyResult, err := coolify.Import([]byte(composeFile))
	require.NoError(t, err)

	dokployResult, err := dokploy.Import([]byte(composeFile))
	require.NoError(t, err)

	composeResult, err := compose.Import([]byte(composeFile))
	require.NoError(t, err)

	for source, result := range map[string]importer.Result{
		"fly.io":  fly,
		"vercel":  vercelResult,
		"coolify": coolifyResult,
		"dokploy": dokployResult,
		"compose": composeResult,
	} {
		for kind, names := range emitted(t, result) {
			engines := schema.EnginesOf(kind)
			if len(engines) == 0 {
				// A kind that runs nothing — a volume, a zone — is not a
				// compute resource and needs no tier. That is a real answer
				// rather than a hole: EnginesOf is empty for exactly those.
				continue
			}

			// A KIND WITH A CHOICE runs if one of its tiers does; which one
			// the resource takes is its `runtime`, checked below where an
			// importer writes it and left to the author where it is a hole.
			runs := false
			for _, engine := range engines {
				require.True(t, implemented[engine] || absent[engine] != "",
					"%s imports %s/%v, which the vocabulary says runs on %q — and nothing implements that engine. "+
						"An imported project would compile, deploy, and refuse every request.",
					source, kind, names, engine)
				runs = runs || implemented[engine]
			}
			require.True(t, runs, "%s imports %s/%v, and no tier its kind may run on is implemented", source, kind, names)
		}

		for _, res := range result.Resources {
			if tier, ok := res.Doc["runtime"].(string); ok {
				require.True(t, implemented[tier], "%s imports %s/%s on the %q tier, and nothing implements it",
					source, res.Kind, res.Name, tier)
			}
		}
	}
}

func TestEveryEngineTheVocabularyNamesIsImplemented(t *testing.T) {
	named := map[string]bool{}
	for _, kind := range schema.Kinds() {
		for _, engine := range schema.EnginesOf(kind) {
			named[engine] = true
		}
	}
	require.NotEmpty(t, named)

	var missing []string
	for engine := range named {
		if !implemented[engine] && absent[engine] == "" {
			missing = append(missing, engine)
		}
	}
	sort.Strings(missing)

	require.Empty(t, missing,
		"the vocabulary names an engine nothing implements: %v. A resource declaring it compiles, "+
			"deploys, and refuses every request with \"no engine registered for this spec\".", missing)

	// and the absent list says only what is still true
	for engine := range absent {
		require.True(t, named[engine], "%q is listed as absent and the vocabulary does not name it", engine)
		require.False(t, implemented[engine], "%q has a tier now; take it off the absent list", engine)
	}
}
