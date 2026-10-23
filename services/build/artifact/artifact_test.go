package artifact_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	pkgregister "github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/backend"
)

const (
	basicFixture = "../../../pkg/tcc/taubyte/v2/fixtures/basic"
	envsFixture  = "../../../pkg/tcc/taubyte/v2/fixtures/environments"

	mainRepo   = "github.com/acme/shop-config"
	paylibRepo = "github.com/acme/paylib"
	projectID  = "shop"
)

// ── the seams, stood up ──────────────────────────────────────────────────

type projects struct {
	main string
	err  error
}

func (p projects) MainRepository(context.Context, string) (string, error) { return p.main, p.err }

type checkout struct {
	dir      string
	err      error
	released bool
}

func (c *checkout) Checkout(context.Context, string, string, string) (string, func(), error) {
	if c.err != nil {
		return "", nil, c.err
	}
	return c.dir, func() { c.released = true }, nil
}

// engine records what it was asked to build and answers with the spec's own
// shape, so a test can see exactly what reached it.
type engine struct {
	mu    sync.Mutex
	specs []artifact.Spec
	err   error
}

func (e *engine) Make(_ context.Context, spec artifact.Spec, log io.Writer) (io.ReadCloser, error) {
	e.mu.Lock()
	e.specs = append(e.specs, spec)
	e.mu.Unlock()

	if e.err != nil {
		return nil, e.err
	}
	fmt.Fprintf(log, "ran %s in %s\n", spec.Image, spec.Dir)
	return io.NopCloser(strings.NewReader("built " + spec.Output)), nil
}

func (e *engine) seen() []artifact.Spec {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]artifact.Spec(nil), e.specs...)
}

// out is a backend.Output that keeps what it was handed.
type out struct {
	mu     sync.Mutex
	log    bytes.Buffer
	assets map[string]string
	err    error
}

func newOut() *out { return &out{assets: map[string]string{}} }

func (o *out) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.log.Write(p)
}

func (o *out) Asset(_ context.Context, environment, resourceID string, content io.Reader) error {
	if o.err != nil {
		return o.err
	}
	body, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	// The same shape the backend composes, so what these tests read back is
	// what a real record holds.
	o.assets[environment+"/"+resourceID] = string(body)
	return nil
}

func (o *out) text() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.log.String()
}

func (o *out) stored() map[string]string {
	o.mu.Lock()
	defer o.mu.Unlock()
	copied := map[string]string{}
	for k, v := range o.assets {
		copied[k] = v
	}
	return copied
}

// ── a register with real compiled projects in it ─────────────────────────

// project writes a small configuration on disk.
//
// IT IS ITS OWN FIXTURE rather than the vocabulary's, because what these tests
// are about is which resources a push touches and what each build is handed —
// and the shared fixtures carry a dockerfile build and a resource with no
// source, both of which are their own cases and are tested as such below.
const everyBranch = "environments:\n    prod:\n        branches: [\"*\"]\n"

func projectConfig(t *testing.T, environments string, resources map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	body := "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nname: shop\n" + environments
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.yaml"), []byte(body), 0o644))

	for path, content := range resources {
		full := filepath.Join(dir, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	return dir
}

// oneEnvironment is a project with a resource in its own repository and one in
// the project's, both built from two commands.
func oneEnvironment(t *testing.T) string {
	t.Helper()
	return projectConfig(t, everyBranch, map[string]string{
		"function/reserve.yaml": "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc\n" +
			"source: services/reserve\n" +
			"build:\n    commands:\n        image: rust:1.83\n" +
			"        install: cargo fetch --locked\n        run: cargo build --release\n" +
			"        output: target/release/reserve.wasm\n" +
			"execution:\n    call: reserve\n    timeout: 15s\n    memory: 32MB\n",
		"component/paylib.yaml": "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\n" +
			"source:\n    git: https://github.com/acme/paylib\n    branch: main\n" +
			"build:\n    script:\n        image: taubyte/go-wasi:v2\n" +
			"        path: .taubyte/build.sh\n        output: paylib.wasm\n",
	})
}

// perEnvironment is one resource whose toolchain differs by deployment.
func perEnvironment(t *testing.T) string {
	t.Helper()
	return projectConfig(t,
		"environments:\n"+
			"    production:\n        branches: [main]\n"+
			"        shadows:\n            ab:\n                branches: [main]\n"+
			"    preview:\n        branches: [\"*\"]\n"+
			"    staging:\n        branches: [\"release/*\"]\n",
		map[string]string{
			"function/reserve.yaml": "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc\n" +
				"source: services/reserve\n" +
				"build:\n    commands:\n        image: rust:1.83\n" +
				"        image_preview: rust:1.83-slim\n" +
				"        run: cargo build --release\n" +
				"        run_preview: cargo build\n" +
				"        output: target/release/reserve.wasm\n" +
				"        output_preview: target/debug/reserve.wasm\n" +
				"execution:\n    call: reserve\n    timeout: 15s\n    memory: 32MB\n",
		})
}

// deployed compiles a configuration for each environment and publishes it,
// which is exactly what a configuration repository's deploy leaves behind.
func deployed(t *testing.T, dir string, environments ...string) *pkgregister.Register {
	t.Helper()

	r, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)

	if len(environments) == 0 {
		environments = []string{"prod"}
	}
	declared, err := schema.EnvironmentsOf(afero.NewOsFs(), dir)
	require.NoError(t, err)

	for _, environment := range environments {
		options := []schema.Option{
			schema.WithLocal(dir),
			schema.WithBranch("main"),
			schema.WithEnvironments(schema.EnvironmentNames(declared)...),
			schema.WithEnvironment(environment),
		}

		c, err := schema.New(options...)
		require.NoError(t, err)

		obj, _, err := c.Compile(t.Context())
		require.NoError(t, err)

		compiled, err := json.Marshal(obj.Flat())
		require.NoError(t, err)

		_, err = r.Publish(t.Context(), pkgregister.Publication{
			Project: projectID, Environment: environment, Revision: 1, Compiled: compiled,
		})
		require.NoError(t, err)
	}
	return r
}

func work(repository string) backend.Work {
	return backend.Work{
		JobID:         "job",
		RepositoryKey: repository,
		Ref:           "refs/heads/main",
		CommitSHA:     "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c",
		ProjectID:     projectID,
		Attempt:       1,
	}
}

func builder(t *testing.T, r artifact.Register, c *checkout, e *engine, main string, opts ...artifact.Option) *artifact.Builder {
	t.Helper()
	b, err := artifact.New(append([]artifact.Option{
		artifact.WithRegister(r),
		artifact.WithProjects(projects{main: main}),
		artifact.WithCheckout(c),
		artifact.WithEngine(e),
	}, opts...)...)
	require.NoError(t, err)
	return b
}

// vault is a Secrets over a map keyed the way the real one is:
// project/environment/name.
type vault map[string]string

func (v vault) Secret(_ context.Context, project, environment, secretID string) (string, error) {
	value, ok := v[project+"/"+environment+"/"+secretID]
	if !ok {
		return "", errNoSuchSecret
	}
	return value, nil
}

var errNoSuchSecret = errors.New("nothing is set under that name")

func source(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "services", "reserve"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "apps", "shop"), 0o755))
	return dir
}

// ── what a push builds ───────────────────────────────────────────────────

// IT BUILDS WHAT THE DEPLOYED CONFIGURATION SAYS THIS REPOSITORY MAKES — from
// the recipe the running deployment declares, not from whatever the config
// repository's HEAD says today.
func TestAPushBuildsWhatTheConfigurationSaysItMakes(t *testing.T) {
	e := &engine{}
	c := &checkout{dir: source(t)}
	o := newOut()

	require.NoError(t, builder(t, deployed(t, oneEnvironment(t)), c, e, mainRepo).
		Build(t.Context(), work(mainRepo), o))

	// Every resource whose source is a bare path in the project's own code
	// repository, and nothing else.
	specs := e.seen()
	require.NotEmpty(t, specs)

	images := map[string]bool{}
	for _, spec := range specs {
		images[spec.Image] = true
	}
	require.True(t, images["rust:1.83"], "function/reserve")
	require.False(t, images["taubyte/go-wasi:v2"], "component/paylib is in ITS OWN repository")

	// Each artifact is filed under its deployment and its resource id.
	stored := o.stored()
	require.NotEmpty(t, stored)
	for key := range stored {
		require.True(t, strings.HasPrefix(key, "prod/"), key)
	}

	require.True(t, c.released, "the checkout is released whatever happened")
}

// A RESOURCE THAT NAMES ITS OWN REPOSITORY IS BUILT BY A PUSH TO THAT ONE, and
// by nothing else.
func TestAResourceWithItsOwnRepository(t *testing.T) {
	e := &engine{}
	o := newOut()

	require.NoError(t, builder(t, deployed(t, oneEnvironment(t)), &checkout{dir: source(t)}, e, mainRepo).
		Build(t.Context(), work(paylibRepo), o))

	specs := e.seen()
	require.Len(t, specs, 1)
	require.Equal(t, "taubyte/go-wasi:v2", specs[0].Image)
	// A script build runs the script the author named.
	require.Equal(t, []string{"sh .taubyte/build.sh"}, specs[0].Commands)
	require.Equal(t, "paylib.wasm", specs[0].Output)
}

// THE COMMANDS ARE THE AUTHOR'S, IN ORDER, and an absent one is absent rather
// than an empty shell invocation.
func TestTheBuildCommandsAreTheAuthors(t *testing.T) {
	e := &engine{}
	o := newOut()

	require.NoError(t, builder(t, deployed(t, oneEnvironment(t)), &checkout{dir: source(t)}, e, mainRepo).
		Build(t.Context(), work(mainRepo), o))

	for _, spec := range e.seen() {
		switch spec.Image {
		case "rust:1.83":
			require.Equal(t, []string{"cargo fetch --locked", "cargo build --release"}, spec.Commands)
			require.Equal(t, "target/release/reserve.wasm", spec.Output)
			require.True(t, strings.HasSuffix(spec.Dir, filepath.Join("services", "reserve")), spec.Dir)
		}
	}
}

// ONE RESOURCE CAN HAVE A DIFFERENT ARTIFACT PER DEPLOYMENT, because a build
// stanza is per-environment — so a key that was only the resource id would have
// preview's build silently overwrite production's.
func TestOneResourceBuildsOncePerDeployment(t *testing.T) {
	e := &engine{}
	o := newOut()

	r := deployed(t, perEnvironment(t), "production", "preview", "staging", "ab")
	require.NoError(t, builder(t, r, &checkout{dir: t.TempDir()}, e, mainRepo).
		Build(t.Context(), work(mainRepo), o))

	// main feeds production, its shadow, and preview — but not staging.
	stored := o.stored()
	require.Contains(t, keysOf(stored), "production")
	require.Contains(t, keysOf(stored), "preview")
	require.Contains(t, keysOf(stored), "ab")
	require.NotContains(t, keysOf(stored), "staging")

	// AND THE TOOLCHAIN DIFFERS, which is the whole reason they are separate.
	images := map[string]bool{}
	for _, spec := range e.seen() {
		images[spec.Image] = true
	}
	require.True(t, images["rust:1.83"], "production's")
	require.True(t, images["rust:1.83-slim"], "preview's")
}

func keysOf(m map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for key := range m {
		env := strings.SplitN(key, "/", 2)[0]
		if !seen[env] {
			seen[env] = true
			out = append(out, env)
		}
	}
	return out
}

// ── what it refuses, and what it merely reports ──────────────────────────

// A PROJECT THAT HAS NEVER BEEN DEPLOYED has no configuration to build against,
// and building against the repository's own guess would be this service
// inventing a recipe.
func TestAProjectThatWasNeverDeployed(t *testing.T) {
	r, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)

	err = builder(t, r, &checkout{dir: t.TempDir()}, &engine{}, mainRepo).
		Build(t.Context(), work(mainRepo), newOut())
	require.ErrorIs(t, err, artifact.ErrNeverDeployed)
}

// A PUSH THAT TOUCHES NOTHING IS NOT A FAILURE. A repository can be registered
// to a project and back nothing in the deployments this branch feeds.
func TestAPushThatTouchesNothing(t *testing.T) {
	e := &engine{}
	c := &checkout{dir: t.TempDir()}
	o := newOut()

	require.NoError(t, builder(t, deployed(t, oneEnvironment(t)), c, e, mainRepo).
		Build(t.Context(), work("github.com/acme/somebody-elses"), o))

	require.Empty(t, e.seen(), "nothing was built")
	require.Empty(t, o.stored())
	require.Contains(t, o.text(), "nothing in")
	require.False(t, c.released, "and nothing was cloned to find that out")
}

func TestWhatItRefuses(t *testing.T) {
	r := deployed(t, oneEnvironment(t))

	// A tag is not a deployment.
	w := work(mainRepo)
	w.Ref = "refs/tags/v1.0.0"
	err := builder(t, r, &checkout{dir: t.TempDir()}, &engine{}, mainRepo).
		Build(t.Context(), w, newOut())
	require.ErrorIs(t, err, artifact.ErrNotABranch)

	// A checkout that could not read the repository names the commit, so a
	// rewritten branch is diagnosable.
	err = builder(t, r, &checkout{err: errors.New("no such ref")}, &engine{}, mainRepo).
		Build(t.Context(), work(mainRepo), newOut())
	require.ErrorContains(t, err, "no such ref")
	require.ErrorContains(t, err, "0f1e2d3c")

	// An engine that could not run says which resource.
	err = builder(t, r, &checkout{dir: source(t)}, &engine{err: errors.New("the daemon is down")}, mainRepo).
		Build(t.Context(), work(mainRepo), newOut())
	require.ErrorContains(t, err, "the daemon is down")

	// A store that could not keep what was built fails the job: an artifact
	// nobody kept is a build that reported success and produced nothing.
	broken := newOut()
	broken.err = errors.New("no space left")
	err = builder(t, r, &checkout{dir: source(t)}, &engine{}, mainRepo).
		Build(t.Context(), work(mainRepo), broken)
	require.ErrorContains(t, err, "no space left")
}

// A PROJECT WITH NO MAIN REPOSITORY KNOWN has no bare-path resources to build,
// and a push claiming to be it builds nothing rather than everything.
func TestNoMainRepositoryKnown(t *testing.T) {
	e := &engine{}
	require.NoError(t, builder(t, deployed(t, oneEnvironment(t)), &checkout{dir: t.TempDir()}, e, "").
		Build(t.Context(), work(mainRepo), newOut()))
	require.Empty(t, e.seen())
}

func TestWhatItCannotBeBuiltWithout(t *testing.T) {
	_, err := artifact.New()
	require.ErrorIs(t, err, artifact.ErrNoRegister)

	r := deployed(t, oneEnvironment(t))
	_, err = artifact.New(artifact.WithRegister(r))
	require.ErrorIs(t, err, artifact.ErrNoProjects)

	_, err = artifact.New(artifact.WithRegister(r), artifact.WithProjects(projects{}))
	require.ErrorIs(t, err, artifact.ErrNoCheckout)

	_, err = artifact.New(artifact.WithRegister(r), artifact.WithProjects(projects{}),
		artifact.WithCheckout(&checkout{}))
	require.ErrorIs(t, err, artifact.ErrNoEngine)

	for _, option := range []artifact.Option{
		artifact.WithRegister(nil), artifact.WithProjects(nil),
		artifact.WithCheckout(nil), artifact.WithEngine(nil),
	} {
		_, err := artifact.New(option)
		require.Error(t, err)
	}
}

// ── the two the vocabulary's own fixtures are ────────────────────────────

// A DOCKERFILE BUILD PRODUCES AN IMAGE, not a file, and an image has to be
// pushed to a registry before anything can run it. This cloud has no registry,
// so the push is refused BY NAME — and the whole push, not just that resource,
// for the reason a deploy compiles everything before publishing anything: a
// partially rebuilt deployment looks exactly like a healthy one.
//
// The vocabulary's own basic project declares one, so this is not a
// hypothetical: it is the first thing a real project hits.
func TestADockerfileBuildIsRefusedByName(t *testing.T) {
	e := &engine{}

	err := builder(t, deployed(t, basicFixture), &checkout{dir: source(t)}, e, mainRepo).
		Build(t.Context(), work(mainRepo), newOut())

	require.ErrorContains(t, err, "dockerfile")
	require.ErrorContains(t, err, "application/worker_pool")
	require.Empty(t, e.seen(), "nothing was built, rather than everything but that one")
}

// A RESOURCE WITH A BUILD AND NO SOURCE IS NOT BUILT FROM THE CHECKOUT ROOT.
// The environments fixture is exactly that shape — it exercises per-deployment
// keys and declares no source — and before pkg/project could tell an absent
// source from a bare path of "", it would have been built from whatever the
// repository root happened to contain.
func TestAResourceWithNoSourceIsNotBuilt(t *testing.T) {
	e := &engine{}

	require.NoError(t, builder(t, deployed(t, envsFixture, "production"), &checkout{dir: t.TempDir()}, e, mainRepo).
		Build(t.Context(), work(mainRepo), newOut()))
	require.Empty(t, e.seen())
}

// ── the path is a stranger's ─────────────────────────────────────────────

// THE COMPILER REFUSES A PATH THAT CLIMBS OUT, which is the first line and the
// one that matters: `IsRepoLocation` rejects `..` where the config is read, so
// a compiled object cannot carry one. joinWithin is the second line, tested in
// path_test.go, for a compiled object that arrived some other way.
func TestAPathThatClimbsOutIsRefusedAtCompileTime(t *testing.T) {
	escaping := projectConfig(t, everyBranch, map[string]string{
		"function/reserve.yaml": "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc\n" +
			"source: ../../../../etc\n" +
			"build:\n    commands:\n        image: rust:1.83\n        run: cat shadow\n" +
			"        output: out\n" +
			"execution:\n    call: reserve\n    timeout: 15s\n    memory: 32MB\n",
	})

	c, err := schema.New(schema.WithLocal(escaping), schema.WithBranch("main"))
	require.NoError(t, err)

	_, _, err = c.Compile(t.Context())
	require.ErrorContains(t, err, "climbs out of the repository")
}

// ── what it cannot find out ──────────────────────────────────────────────

func TestWhenItCannotAskAuth(t *testing.T) {
	b, err := artifact.New(
		artifact.WithRegister(deployed(t, oneEnvironment(t))),
		artifact.WithProjects(projects{err: errors.New("auth is down")}),
		artifact.WithCheckout(&checkout{dir: t.TempDir()}),
		artifact.WithEngine(&engine{}),
	)
	require.NoError(t, err)

	require.ErrorContains(t, b.Build(t.Context(), work(mainRepo), newOut()), "auth is down")
}

func TestWhenItCannotReadTheRegister(t *testing.T) {
	b, err := artifact.New(
		artifact.WithRegister(unreachable{}),
		artifact.WithProjects(projects{main: mainRepo}),
		artifact.WithCheckout(&checkout{dir: t.TempDir()}),
		artifact.WithEngine(&engine{}),
	)
	require.NoError(t, err)

	err = b.Build(t.Context(), work(mainRepo), newOut())
	require.ErrorContains(t, err, "the register is unreachable")
	require.NotErrorIs(t, err, artifact.ErrNeverDeployed,
		"a register that cannot be read is not a project that was never deployed")
}

type unreachable struct{}

func (unreachable) Environments(context.Context, string) ([]string, error) {
	return nil, errors.New("the register is unreachable")
}
func (unreachable) Resolve(context.Context, string, string) (pkgregister.Pointer, error) {
	return pkgregister.Pointer{}, errors.New("the register is unreachable")
}
func (unreachable) Object(context.Context, pkgregister.ObjectID) ([]byte, error) {
	return nil, errors.New("the register is unreachable")
}

// A BRANCH THE CONFIGURATION DECLARES AND NOBODY HAS DEPLOYED builds nothing,
// because an artifact filed under a pointer no worker will ever read is a build
// that looks like it worked.
func TestABranchWhoseDeploymentIsNotLive(t *testing.T) {
	// Only production is published; release/* feeds staging, which is not.
	r := deployed(t, perEnvironment(t), "production")

	w := work(mainRepo)
	w.Ref = "refs/heads/release/1.4"

	err := builder(t, r, &checkout{dir: t.TempDir()}, &engine{}, mainRepo).
		Build(t.Context(), w, newOut())
	require.ErrorIs(t, err, artifact.ErrNoDeployment)
	require.ErrorContains(t, err, "none of those is deployed")
}

// A BRANCH NO ENVIRONMENT IS FED BY builds nothing, and says which ones exist.
func TestABranchThatFeedsNoDeployment(t *testing.T) {
	r := deployed(t, projectConfig(t, "environments:\n    production:\n        branches: [main]\n", nil), "production")

	w := work(mainRepo)
	w.Ref = "refs/heads/wip"

	err := builder(t, r, &checkout{dir: t.TempDir()}, &engine{}, mainRepo).
		Build(t.Context(), w, newOut())
	require.ErrorIs(t, err, artifact.ErrNoDeployment)
	require.ErrorContains(t, err, `"wip" feeds none of production`)
}

// ── the build's own environment ──────────────────────────────────────────

// A BUILD-TIME VARIABLE REACHES THE BUILD AND A RUN-TIME ONE DOES NOT. Passing
// everything would put a runtime secret into a toolchain container somebody
// else wrote.
func TestOnlyBuildTimeVariablesReachTheBuild(t *testing.T) {
	config := projectConfig(t, everyBranch, map[string]string{
		"function/reserve.yaml": "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc\n" +
			"source: services/reserve\n" +
			"build:\n    commands:\n        image: rust:1.83\n        run: cargo build\n" +
			"        output: out.wasm\n" +
			"execution:\n    call: reserve\n    timeout: 15s\n    memory: 32MB\n" +
			"variables:\n" +
			"    GOPRIVATE:\n        at: build\n        value: github.com/acme/*\n" +
			"    API_BASE:\n        at: run\n        value: https://api.example.com\n",
	})

	e := &engine{}
	require.NoError(t, builder(t, deployed(t, config), &checkout{dir: t.TempDir()}, e, mainRepo).
		Build(t.Context(), work(mainRepo), newOut()))

	specs := e.seen()
	require.Len(t, specs, 1)
	require.Equal(t, map[string]string{"GOPRIVATE": "github.com/acme/*"}, specs[0].Variables)
}

// A SECRET NEEDS A RESOLVER AND THERE IS NONE, so the build is refused rather
// than run without it — a build that quietly lost its token fails somewhere
// further in, with a message about a dependency it could not fetch.
func TestABuildThatNeedsASecret(t *testing.T) {
	e := &engine{}

	err := builder(t, deployed(t, basicFixture), &checkout{dir: source(t)}, e, mainRepo).
		Build(t.Context(), work(paylibRepo), newOut())

	require.ErrorIs(t, err, artifact.ErrNoSecrets)
	require.ErrorContains(t, err, "GITHUB_TOKEN")
	require.Empty(t, e.seen())
}

// TestABuildTimeSecretReachesTheBuild, which is the private-dependency token
// and the registry credential — the thing between an imported project and a
// build that completes.
func TestABuildTimeSecretReachesTheBuild(t *testing.T) {
	e := &engine{}
	held := vault{projectID + "/prod/QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy": "ghp_thetoken"}

	require.NoError(t, builder(t, deployed(t, basicFixture), &checkout{dir: source(t)}, e, mainRepo,
		artifact.WithSecrets(held)).
		Build(t.Context(), work(paylibRepo), newOut()))

	specs := e.seen()
	require.Len(t, specs, 1)
	require.Equal(t, "ghp_thetoken", specs[0].Variables["GITHUB_TOKEN"])
}

// TestAVaultThatRefusesFailsTheBuildBeforeAnythingRuns. Every spec is read
// before any build runs, so a resource whose secret cannot be resolved must
// not leave the others rebuilt and that one stale.
func TestAVaultThatRefusesFailsTheBuildBeforeAnythingRuns(t *testing.T) {
	e := &engine{}

	err := builder(t, deployed(t, basicFixture), &checkout{dir: source(t)}, e, mainRepo,
		artifact.WithSecrets(vault{})).
		Build(t.Context(), work(paylibRepo), newOut())

	require.ErrorIs(t, err, errNoSuchSecret)
	require.ErrorContains(t, err, "GITHUB_TOKEN")
	require.Empty(t, e.seen(), "nothing was built")
}

// TestTheValueNeverReachesTheJobLog. This error is written into the job
// record, which everyone who can read the repository can read.
func TestTheValueNeverReachesTheJobLog(t *testing.T) {
	out := newOut()
	err := builder(t, deployed(t, basicFixture), &checkout{dir: source(t)}, &engine{}, mainRepo,
		artifact.WithSecrets(vault{})).
		Build(t.Context(), work(paylibRepo), out)

	require.Error(t, err)
	require.NotContains(t, err.Error(), "ghp_")
}

func TestASecretsResolverMustNotBeNil(t *testing.T) {
	_, err := artifact.New(
		artifact.WithRegister(deployed(t, basicFixture)),
		artifact.WithProjects(projects{main: mainRepo}),
		artifact.WithCheckout(&checkout{dir: t.TempDir()}),
		artifact.WithEngine(&engine{}),
		artifact.WithSecrets(nil),
	)
	require.ErrorContains(t, err, "secrets must not be nil")
}

// A RESOURCE WHOSE SERVING BLOCK IS DELEGATED TO A FILE THE COMPILE DID NOT
// READ IS REFUSED BY NAME, rather than built and served by the block that file
// was meant to replace.
func TestAnUnreadDelegatedBlockIsRefusedByName(t *testing.T) {
	delegated := projectConfig(t, everyBranch, map[string]string{
		"component/paylib.yaml": "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\n" +
			"source:\n    git: https://github.com/acme/paylib\n    branch: main\n    config: taubyte.yaml\n" +
			"build:\n    script:\n        image: taubyte/go-wasi:v2\n" +
			"        path: .taubyte/build.sh\n        output: paylib.wasm\n",
	})
	e := &engine{}

	err := builder(t, deployed(t, delegated), &checkout{dir: source(t)}, e, mainRepo).
		Build(t.Context(), work(paylibRepo), newOut())
	require.ErrorIs(t, err, artifact.ErrUnfilled)
	require.ErrorContains(t, err, `component/paylib names source.config "taubyte.yaml"`)
	require.Empty(t, e.seen())
}

// A BARE PATH IS THE MAIN REPOSITORY, never a code repository, however many
// the project links: pushing the configuration repository builds the bare-path
// resource, pushing a code repository no source names builds nothing, and
// pushing the one a source names by git URL builds only that resource.
func TestABarePathIsTheMainRepositoryNotACodeRepository(t *testing.T) {
	r := deployed(t, oneEnvironment(t))
	for _, c := range []struct {
		pushed string
		images []string
	}{
		{mainRepo, []string{"rust:1.83"}},
		{"github.com/acme/shop-code", nil},
		{"github.com/acme/another-code", nil},
		{paylibRepo, []string{"taubyte/go-wasi:v2"}},
	} {
		e := &engine{}
		require.NoError(t, builder(t, r, &checkout{dir: source(t)}, e, mainRepo).
			Build(t.Context(), work(c.pushed), newOut()), c.pushed)
		var images []string
		for _, s := range e.seen() {
			images = append(images, s.Image)
		}
		require.Equal(t, c.images, images, c.pushed)
	}
}
