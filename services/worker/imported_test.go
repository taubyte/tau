package worker

import (
	"context"
	"encoding/json"
	"testing"

	"strings"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/importer"
	"github.com/taubyte/tau/pkg/importer/compose"
	"github.com/taubyte/tau/pkg/importer/coolify"
	"github.com/taubyte/tau/pkg/importer/dokploy"
	"github.com/taubyte/tau/pkg/importer/emit"
	"github.com/taubyte/tau/pkg/importer/fly"
	"github.com/taubyte/tau/pkg/importer/lambda"
	"github.com/taubyte/tau/pkg/importer/vercel"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// imported_test.go is the competitive claim end to end: a descriptor from
// somebody else's platform, through an importer, into a config repo, through
// tcc, into the register, out as something this cloud can START.
//
// EVERY OTHER TEST OF THIS STOPS ONE STEP SHORT. pkg/importer/emit proves the
// files are written and that tcc reads them; pkg/importer/emit/runnable_test
// proves every KIND an importer emits has a tier. Neither says the RESOURCE is
// runnable — that the spec a worker resolves carries a runtime, an artifact and
// the numbers a tier needs — and "it compiled" is exactly the answer that made
// the last four defects in this tree look like successes.
//
// It lives here rather than in pkg/importer because a package must not import a
// service. The worker is what turns a compiled resource into something a tier
// runs, so the claim belongs on this side of that line.

// theFlyFile, theVercelOutput and theCompose file are one representative
// descriptor each — enough that every importer emits its kinds.
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

const theComposeFile = `
services:
  api:
    image: ghcr.io/acme/api:1.4
    ports:
      - "8080:8080"
    environment:
      LOG_LEVEL: debug
`

// theSAMTemplate is a Lambda packaged as a container image, which is the one
// AWS shape that maps onto a tier this cloud has.
const theSAMTemplate = `
Resources:
  Img:
    Type: AWS::Serverless::Function
    Properties:
      PackageType: Image
      ImageUri: 1234.dkr.ecr.us-east-1.amazonaws.com/app:1
      MemorySize: 512
      Timeout: 30
`

// filled is a value for one field an import could not supply — what the person
// reading the to-do list would type. There is nothing clever here: the point is
// that the LIST is complete, not that these are good values.
var filled = map[string]any{
	"build/commands/image":  "node:22",
	"build/script/image":    "node:22",
	"build/commands/run":    "npm run build",
	"build/commands/output": "dist",
	"execution/call":        "handler",
	"runtime":               "wasm",
	"execution/memory":      "512MB",
	"execution/timeout":     "30s",
	"source/image":          "ghcr.io/acme/placeholder:1",
	"quota":                 "1GB",
	"size":                  "512MB",
	"consensus":             "crdt",
}

// imported runs one importer's output all the way to a published project,
// FILLING IN EXACTLY WHAT THE IMPORT SAID IT COULD NOT.
//
// THAT IS THE CLAIM, and it is stronger than "it compiled". An importer that
// wrote a hole and did not list it produces a project a person cannot fix,
// because nothing tells them what is wrong until tcc refuses the whole thing
// with one error at a time. So the to-do list is filled from `filled` above and
// nothing else — if the project still does not compile, the list was
// incomplete, which is the defect this exists to find.
func imported(t *testing.T, results ...importer.Result) *register.Register {
	t.Helper()

	fs := afero.NewMemMapFs()
	report, err := emit.To(fs, "/project", "shop", results...)
	require.NoError(t, err)

	session, err := schema.AdoptSession(afero.NewBasePathFs(fs, "/project"))
	require.NoError(t, err)

	for _, missing := range report.Missing {
		value, ok := filled[missing.Field]
		require.True(t, ok,
			"the import needs %s on %s/%s and this test has no value for it — add one",
			missing.Field, missing.Kind, missing.Name)

		require.NoError(t,
			session.Set([]string{missing.Kind, missing.Name}, strings.Split(missing.Field, "/"), value),
			"%s", missing)
	}
	if len(report.Missing) > 0 {
		require.NoError(t, session.Save(session.FS(), "/"))
	}

	// THE REAL COMPILER over what the importer wrote plus the to-do list.
	// Anything it refuses now is an importer emitting config this platform
	// cannot read, or a hole it did not report — either way a defect in the
	// importer and not in this test.
	c, err := schema.New(schema.WithVirtual(fs, "/project"), schema.WithBranch("main"))
	require.NoError(t, err)

	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	raw, err := json.Marshal(obj.Flat())
	require.NoError(t, err)

	store := register.NewMemStore()
	reg, err := register.New(register.WithStore(store))
	require.NoError(t, err)

	_, err = reg.Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: raw,
	})
	require.NoError(t, err)
	return reg
}

// runnable resolves every workload an imported project declares and says what
// this cloud would do with it.
func runnable(t *testing.T, reg *register.Register) map[string]invoke.Spec {
	t.Helper()

	h := build(t, Config{}, resolvers()...)
	h.Register.Register = reg

	proj, _, err := h.Worker.projects.At(t.Context(), register.Deployment{Project: "shop", Environment: "prod"})
	require.NoError(t, err)

	out := map[string]invoke.Spec{}
	for _, kind := range []string{"website", "function", "application", "worker", "sandbox"} {
		names, err := proj.Names(nil, kind)
		require.NoError(t, err)

		for _, name := range names {
			spec, err := h.Worker.Resolve(t.Context(), Target{
				Project: "shop", Environment: "prod", Kind: kind, Name: name,
			})
			require.NoError(t, err, "%s/%s resolved to nothing this cloud can run", kind, name)
			out[kind+"/"+name] = spec
		}
	}
	return out
}

// assertRunnable is what "this cloud can start it" means, field by field.
func assertRunnable(t *testing.T, specs map[string]invoke.Spec) {
	t.Helper()
	require.NotEmpty(t, specs, "an import that yields no workload imported nothing")

	for name, spec := range specs {
		require.NotEmpty(t, spec.Runtime, "%s: no tier would be selected", name)
		require.Contains(t, implementedTiers, spec.Runtime,
			"%s: the vocabulary chose %q and nothing implements it", name, spec.Runtime)

		// AN ARTIFACT IS WHAT A TIER FETCHES. Empty means the resource named
		// no image and no component and the build seam answered with nothing —
		// which starts and then refuses every request.
		require.NotEmpty(t, spec.Artifact, "%s: nothing to run", name)
		require.NotEmpty(t, spec.Name, "%s: unnamed", name)
		require.NotEmpty(t, spec.Revision, "%s: a spec with no revision cannot be told from the next deploy", name)
	}
}

// implementedTiers is the same set pkg/importer/emit/runnable_test holds,
// restated because this file asserts a different thing about it: not that the
// kind names an implemented engine, but that the resolved SPEC did.
var implementedTiers = []string{"container", "static", "wasm"}

// ── one test per platform ────────────────────────────────────────────────────

func TestAFlyAppIsRunnable(t *testing.T) {
	result, err := fly.Import([]byte(theFlyFile))
	require.NoError(t, err)
	assertRunnable(t, runnable(t, imported(t, result)))
}

func TestAVercelProjectIsRunnable(t *testing.T) {
	result, err := vercel.Import([]byte(theVercelOutput))
	require.NoError(t, err)
	assertRunnable(t, runnable(t, imported(t, result)))
}

func TestAComposeProjectIsRunnable(t *testing.T) {
	result, err := compose.Import([]byte(theComposeFile))
	require.NoError(t, err)
	assertRunnable(t, runnable(t, imported(t, result)))
}

func TestACoolifyAppIsRunnable(t *testing.T) {
	result, err := coolify.Import([]byte(theComposeFile))
	require.NoError(t, err)
	assertRunnable(t, runnable(t, imported(t, result)))
}

func TestADokployAppIsRunnable(t *testing.T) {
	result, err := dokploy.Import([]byte(theComposeFile))
	require.NoError(t, err)
	assertRunnable(t, runnable(t, imported(t, result)))
}

// TestSeveralPlatformsBecomeOneRunnableProject, which is what a migration off
// two of them actually looks like.
func TestTwoImportsBecomeOneRunnableProject(t *testing.T) {
	flyResult, err := fly.Import([]byte(theFlyFile))
	require.NoError(t, err)
	vercelResult, err := vercel.Import([]byte(theVercelOutput))
	require.NoError(t, err)

	specs := runnable(t, imported(t, flyResult, vercelResult))
	assertRunnable(t, specs)
	require.Greater(t, len(specs), 1, "both platforms' workloads are there")
}

// TestALambdaImageRunsOnTheContainerTier.
//
// A container-packaged Lambda is the reason the container engine exists as a
// serverless tier at all, and it used to be the one import this platform could
// not finish: the image embeds AWS's runtime interface client, which polls for
// work rather than listening. The tier now serves that protocol
// (pkg/invoke/container/lambda.go), so the image runs unmodified.
//
// WHAT IS ASSERTED IS BOTH HALVES. The resource is emitted on the right tier
// with the image the file named — and what still DIFFERS is reported, because
// the event a handler receives is an HTTP one and a handler written for an S3
// notification will not recognise it.
func TestALambdaImageStartsOnTheContainerTier(t *testing.T) {
	result, err := lambda.Import([]byte(theSAMTemplate))
	require.NoError(t, err)

	specs := runnable(t, imported(t, result))
	assertRunnable(t, specs)

	var sawContainer bool
	for _, spec := range specs {
		if spec.Runtime == "container" {
			sawContainer = true
			require.Equal(t, "1234.dkr.ecr.us-east-1.amazonaws.com/app:1", spec.Artifact,
				"the image the template named")
		}
	}
	require.True(t, sawContainer, "a Lambda image runs on the container tier: %v", specs)

	// AND IT SAYS WHAT STILL DIFFERS. An import that runs but hands a handler
	// an event it was never written for fails inside the handler, where the
	// reason is hardest to see.
	var reported bool
	for _, f := range result.Findings {
		if f.Code == "IMAGE_INVOKED_OVER_HTTP" {
			reported = true
			require.Equal(t, importer.Approximated, f.Level, "it runs")
			require.Contains(t, f.Detail, "runs unmodified")
			require.Contains(t, f.Detail, "API Gateway payload format 2.0")
			require.Contains(t, f.Detail, "S3", "and which handlers will not recognise it")
		}
	}
	require.True(t, reported, "what still differs was not reported")
}
