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

// A foreign descriptor through an importer, tcc and the register, out as something
// this cloud can start: the resolved spec carries a runtime, an artifact and the
// numbers a tier needs. It lives here because a package must not import a service.

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

// filled is a value for each field an import could not supply; the point is that
// the to-do list is complete.
var filled = map[string]any{
	"type":              "wasm",
	"execution/call":    "handler",
	"execution/memory":  "512MB",
	"execution/timeout": "30s",
	"quota":             "1GB",
	"size":              "512MB",
	"consensus":         "crdt",
}

// imported runs one importer's output to a published project, filling in exactly
// what the import said it could not. If it still does not compile, the to-do list
// was incomplete.
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

	// Anything the compiler refuses here is an importer defect: unreadable config or an
	// unreported hole.
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
	for _, kind := range []string{"function", "service", "worker", "sandbox"} {
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

		// Empty means no image, no component and nothing from the build seam.
		require.NotEmpty(t, spec.Artifact, "%s: nothing to run", name)
		require.NotEmpty(t, spec.Name, "%s: unnamed", name)
		require.NotEmpty(t, spec.Revision, "%s: a spec with no revision cannot be told from the next deploy", name)
	}
}

// implementedTiers restates pkg/importer/emit/runnable_test's set; here it is the
// resolved spec, not the kind, that must name one.
var implementedTiers = []string{"oci", "warp", "wasm"}

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

func TestTwoImportsBecomeOneRunnableProject(t *testing.T) {
	flyResult, err := fly.Import([]byte(theFlyFile))
	require.NoError(t, err)
	vercelResult, err := vercel.Import([]byte(theVercelOutput))
	require.NoError(t, err)

	specs := runnable(t, imported(t, flyResult, vercelResult))
	assertRunnable(t, specs)
	require.Greater(t, len(specs), 1, "both platforms' workloads are there")
}

func TestALambdaImageStartsOnTheContainerTier(t *testing.T) {
	result, err := lambda.Import([]byte(theSAMTemplate))
	require.NoError(t, err)

	specs := runnable(t, imported(t, result))
	assertRunnable(t, specs)

	var sawContainer bool
	for _, spec := range specs {
		if spec.Runtime == "oci" {
			sawContainer = true
			require.Equal(t, "1234.dkr.ecr.us-east-1.amazonaws.com/app:1", spec.Artifact,
				"the image the template named")
		}
	}
	require.True(t, sawContainer, "a Lambda image runs on the container tier: %v", specs)

	// An import that runs but hands a handler an unexpected event fails inside the
	// handler, so the differences are reported.
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
