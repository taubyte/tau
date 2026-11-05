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
	"github.com/taubyte/tau/pkg/vault"
	"github.com/taubyte/tau/services/plm/artifact"
	"github.com/taubyte/tau/services/plm/run"
)

const (
	basicFixture = "../../../pkg/tcc/taubyte/v2/fixtures/basic"
	envsFixture  = "../../../pkg/tcc/taubyte/v2/fixtures/environments"

	mainRepo   = "github.com/acme/shop-config"
	paylibRepo = "github.com/acme/paylib"
	projectID  = "shop"
)

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

// engine records what it was asked to plm and answers with the spec's own
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
	fmt.Fprintf(log, "ran %s in %s\n", imageOf(spec), spec.Dir)
	return io.NopCloser(strings.NewReader("built " + spec.Output)), nil
}

// imageOf is the toolchain of a recipe's first step, which is how these tests tell
// one recipe from another.
func imageOf(spec artifact.Spec) string {
	if len(spec.Steps) == 0 {
		return ""
	}
	return spec.Steps[0].Image
}

func (e *engine) seen() []artifact.Spec {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]artifact.Spec(nil), e.specs...)
}

// out is a run.Output that keeps what it was handed.
type out struct {
	mu     sync.Mutex
	log    bytes.Buffer
	assets map[string]string
	err    error
	// environment is the one the job being run builds, as the builder's output knows.
	environment string
}

func newOut() *out { return &out{assets: map[string]string{}} }

func (o *out) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.log.Write(p)
}

func (o *out) Asset(_ context.Context, resourceID string, content io.Reader) error {
	if o.err != nil {
		return o.err
	}
	body, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	// The same shape a builder composes, so what these tests read back is
	// what a real record holds.
	o.assets[o.environment+"/"+resourceID] = string(body)
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

// Its own fixture: the shared ones carry a dockerfile plm and a sourceless
// package, tested separately below.
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

// steps is a package's build of one step.
func steps(image string, commands ...string) string {
	out := "build:\n    steps:\n        - name: build\n          image: " + image + "\n          commands:\n"
	for _, c := range commands {
		out += "              - " + c + "\n"
	}
	return out
}

// oneEnvironment is a project with a package in its own repository and one in the
// project's.
func oneEnvironment(t *testing.T) string {
	t.Helper()
	return projectConfig(t, everyBranch, map[string]string{
		"function/reserve.yaml": "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc\npackage:\n    name: reserve_code\n" +
			"execution:\n    call: reserve\n    timeout: 15s\n    memory: 32MB\n",
		"package/reserve_code.yaml": "id: QmbB2PpNzfnQ3wAJykt9UZU2SRrFNSzawU16yo9mdpM2Wh\ntype: wasm\n" +
			"source: services/reserve\noutput: target/release/reserve.wasm\n" +
			steps("rust:1.83", "cargo fetch --locked", "cargo build --release"),
		"package/paylib.yaml": "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\ntype: wasm\n" +
			"source:\n    git: https://github.com/acme/paylib\n    branch: main\noutput: paylib.wasm\n" +
			steps("taubyte/go-wasi:v2", ".taubyte/build.sh"),
	})
}

// perEnvironment is one package whose build differs by deployment: in a value its
// recipe reads, and in where the artifact lands.
func perEnvironment(t *testing.T) string {
	t.Helper()
	return projectConfig(t,
		"environments:\n"+
			"    production:\n        branches: [main]\n"+
			"        shadows:\n            ab:\n                branches: [main]\n"+
			"    preview:\n        branches: [\"*\"]\n"+
			"    staging:\n        branches: [\"release/*\"]\n",
		map[string]string{
			"function/reserve.yaml": "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc\npackage:\n    name: reserve_code\n" +
				"execution:\n    call: reserve\n    timeout: 15s\n    memory: 32MB\n",
			"package/reserve_code.yaml": "id: QmbB2PpNzfnQ3wAJykt9UZU2SRrFNSzawU16yo9mdpM2Wh\ntype: wasm\n" +
				"source: services/reserve\n" +
				"output: target/release/reserve.wasm\noutput_preview: target/debug/reserve.wasm\n" +
				"variables:\n    PROFILE:\n        value: release\n        value_preview: dev\n" +
				steps("rust:1.83", "cargo build --profile $PROFILE"),
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

// planned is a push as build runs it: the plan, then one job per environment
// it names, each told its own.
func planned(ctx context.Context, b *artifact.Builder, w run.Work, o *out) error {
	environments, err := b.Plan(ctx, w, o)
	if err != nil {
		return err
	}
	for _, environment := range environments {
		o.mu.Lock()
		o.environment = environment
		o.mu.Unlock()
		w.Environment = environment
		if err := b.Build(ctx, w, o); err != nil {
			return err
		}
	}
	return nil
}

func work(repository string) run.Work {
	return run.Work{
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

func source(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "services", "reserve"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "apps", "shop"), 0o755))
	return dir
}

func TestAPushBuildsWhatTheConfigurationSaysItMakes(t *testing.T) {
	e := &engine{}
	c := &checkout{dir: source(t)}
	o := newOut()

	require.NoError(t, planned(t.Context(), builder(t, deployed(t, oneEnvironment(t)), c, e, mainRepo), work(mainRepo), o))

	// Every resource whose source is a bare path in the project's own code
	// repository, and nothing else.
	specs := e.seen()
	require.NotEmpty(t, specs)

	images := map[string]bool{}
	for _, spec := range specs {
		images[imageOf(spec)] = true
	}
	require.True(t, images["rust:1.83"], "package/reserve_code")
	require.False(t, images["taubyte/go-wasi:v2"], "package/paylib is in ITS OWN repository")

	// Each artifact is filed under its deployment and its resource id.
	stored := o.stored()
	require.NotEmpty(t, stored)
	for key := range stored {
		require.True(t, strings.HasPrefix(key, "prod/"), key)
	}

	require.True(t, c.released, "the checkout is released whatever happened")
}

func TestTheBuildIsToldWhatThePackageIs(t *testing.T) {
	dir := projectConfig(t, everyBranch, map[string]string{
		"package/reserve_code.yaml": "id: QmbB2PpNzfnQ3wAJykt9UZU2SRrFNSzawU16yo9mdpM2Wh\ntype: wasm\n" +
			"source: services/reserve\noutput: target/release/reserve.wasm\n" +
			steps("rust:1.83", "cargo build --release"),
		"package/answer_code.yaml": "id: QmaSQRCEX9SX2BGXksrfSGXvxyFhxspcVS2fB2Z3dmeYex\ntype: warp\n" +
			"source: services/answer\noutput: dist/worker.js\n" +
			steps("node:22", "npx esbuild src/index.js --bundle --format=esm --outfile=dist/worker.js"),
	})
	e := &engine{}
	require.NoError(t, planned(t.Context(), builder(t, deployed(t, dir), &checkout{dir: source(t)}, e, mainRepo), work(mainRepo), newOut()))

	types := map[string]string{}
	for _, spec := range e.seen() {
		types[spec.Output] = spec.Type
	}
	require.Equal(t, map[string]string{
		"target/release/reserve.wasm": "wasm",
		"dist/worker.js":              "warp",
	}, types)
}

// One package, a step per toolchain, in order over one checkout.
func TestEachStepNamesItsOwnToolchain(t *testing.T) {
	dir := projectConfig(t, everyBranch, map[string]string{
		"package/reserve_code.yaml": "id: QmbB2PpNzfnQ3wAJykt9UZU2SRrFNSzawU16yo9mdpM2Wh\ntype: wasm\n" +
			"source: services/reserve\noutput: out.wasm\n" +
			"build:\n    steps:\n" +
			"        - name: generate\n          image: bufbuild/buf:1.47\n          commands: [buf generate]\n" +
			"        - name: compile\n          image: tinygo/tinygo:0.34\n          commands: [tinygo build -o out.wasm]\n",
	})
	e := &engine{}
	require.NoError(t, planned(t.Context(), builder(t, deployed(t, dir), &checkout{dir: source(t)}, e, mainRepo), work(mainRepo), newOut()))

	seen := e.seen()
	require.Len(t, seen, 1, "one package is one build")
	require.Equal(t, []artifact.Step{
		{Name: "generate", Image: "bufbuild/buf:1.47", Commands: []string{"buf generate"}},
		{Name: "compile", Image: "tinygo/tinygo:0.34", Commands: []string{"tinygo build -o out.wasm"}},
	}, seen[0].Steps)
}

func TestAPackageWithItsOwnRepository(t *testing.T) {
	e := &engine{}
	o := newOut()

	require.NoError(t, planned(t.Context(), builder(t, deployed(t, oneEnvironment(t)), &checkout{dir: source(t)}, e, mainRepo), work(paylibRepo), o))

	specs := e.seen()
	require.Len(t, specs, 1)
	require.Equal(t, "taubyte/go-wasi:v2", imageOf(specs[0]))
	// A script is a step like any other: the author names it.
	require.Equal(t, []string{".taubyte/build.sh"}, specs[0].Steps[0].Commands)
	require.Equal(t, "paylib.wasm", specs[0].Output)
}

func TestTheBuildCommandsAreTheAuthors(t *testing.T) {
	e := &engine{}
	o := newOut()

	require.NoError(t, planned(t.Context(), builder(t, deployed(t, oneEnvironment(t)), &checkout{dir: source(t)}, e, mainRepo), work(mainRepo), o))

	for _, spec := range e.seen() {
		switch imageOf(spec) {
		case "rust:1.83":
			require.Equal(t, []string{"cargo fetch --locked", "cargo build --release"}, spec.Steps[0].Commands)
			require.Equal(t, "target/release/reserve.wasm", spec.Output)
			require.Equal(t, filepath.Join("services", "reserve"), spec.Path, "the commands run in the package's own directory")
			require.DirExists(t, filepath.Join(spec.Dir, "services"), "of a checkout mounted whole, so a sibling it depends on is there")
		}
	}
}

func TestOnePackageBuildsOncePerDeployment(t *testing.T) {
	e := &engine{}
	o := newOut()

	r := deployed(t, perEnvironment(t), "production", "preview", "staging", "ab")
	require.NoError(t, planned(t.Context(), builder(t, r, &checkout{dir: t.TempDir()}, e, mainRepo), work(mainRepo), o))

	// main feeds production, its shadow, and preview — but not staging.
	stored := o.stored()
	require.Contains(t, keysOf(stored), "production")
	require.Contains(t, keysOf(stored), "preview")
	require.Contains(t, keysOf(stored), "ab")
	require.NotContains(t, keysOf(stored), "staging")

	// AND WHAT THE RECIPE READS DIFFERS, which is the whole reason they are separate.
	profiles := map[string]string{}
	for _, spec := range e.seen() {
		profiles[spec.Variables["PROFILE"]] = spec.Output
	}
	require.Equal(t, "target/release/reserve.wasm", profiles["release"], "production's")
	require.Equal(t, "target/debug/reserve.wasm", profiles["dev"], "preview's")
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

func TestAProjectThatWasNeverDeployed(t *testing.T) {
	r, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)

	err = planned(t.Context(), builder(t, r, &checkout{dir: t.TempDir()}, &engine{}, mainRepo), work(mainRepo), newOut())
	require.ErrorIs(t, err, artifact.ErrNeverDeployed)
}

func TestAPushThatTouchesNothing(t *testing.T) {
	e := &engine{}
	c := &checkout{dir: t.TempDir()}
	o := newOut()

	require.NoError(t, planned(t.Context(), builder(t, deployed(t, oneEnvironment(t)), c, e, mainRepo), work("github.com/acme/somebody-elses"), o))

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
	err := planned(t.Context(), builder(t, r, &checkout{dir: t.TempDir()}, &engine{}, mainRepo), w, newOut())
	require.ErrorIs(t, err, artifact.ErrNotABranch)

	// A checkout that could not read the repository names the commit, so a
	// rewritten branch is diagnosable.
	err = planned(t.Context(), builder(t, r, &checkout{err: errors.New("no such ref")}, &engine{}, mainRepo), work(mainRepo), newOut())
	require.ErrorContains(t, err, "no such ref")
	require.ErrorContains(t, err, "0f1e2d3c")

	// An engine that could not run says which resource.
	err = planned(t.Context(), builder(t, r, &checkout{dir: source(t)}, &engine{err: errors.New("the daemon is down")}, mainRepo), work(mainRepo), newOut())
	require.ErrorContains(t, err, "the daemon is down")

	// A store that could not keep what was built fails the job: an artifact
	// nobody kept is a build that reported success and produced nothing.
	broken := newOut()
	broken.err = errors.New("no space left")
	err = planned(t.Context(), builder(t, r, &checkout{dir: source(t)}, &engine{}, mainRepo), work(mainRepo), broken)
	require.ErrorContains(t, err, "no space left")
}

func TestNoMainRepositoryKnown(t *testing.T) {
	e := &engine{}
	require.NoError(t, planned(t.Context(), builder(t, deployed(t, oneEnvironment(t)), &checkout{dir: t.TempDir()}, e, ""), work(mainRepo), newOut()))
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

func TestADockerfileBuildIsRefusedByName(t *testing.T) {
	e := &engine{}
	config := projectConfig(t, everyBranch, map[string]string{
		"package/reserve_code.yaml": "id: QmbB2PpNzfnQ3wAJykt9UZU2SRrFNSzawU16yo9mdpM2Wh\ntype: wasm\n" +
			"source: services/reserve\noutput: out.wasm\n" + steps("rust:1.83", "cargo build"),
		"package/worker_pool_code.yaml": "id: QmPzkkzbkX2TYHQsuoJ67gFL65t84vNLcFNSXtjLfvAge4\ntype: oci\n" +
			"source: services/pool\nbuild:\n    dockerfile:\n        target: runtime\n",
	})

	err := planned(t.Context(), builder(t, deployed(t, config), &checkout{dir: source(t)}, e, mainRepo), work(mainRepo), newOut())

	require.ErrorIs(t, err, artifact.ErrNotBuiltHere)
	require.ErrorContains(t, err, "package/worker_pool_code is built from a Dockerfile")
	require.Empty(t, e.seen(), "nothing was built, rather than everything but that one")
}

func TestAPackageWithNoSourceIsNotBuilt(t *testing.T) {
	e := &engine{}

	require.NoError(t, planned(t.Context(), builder(t, deployed(t, envsFixture, "production"), &checkout{dir: t.TempDir()}, e, mainRepo), work(mainRepo), newOut()))
	require.Empty(t, e.seen())
}

func TestAPathThatClimbsOutIsRefusedAtCompileTime(t *testing.T) {
	escaping := projectConfig(t, everyBranch, map[string]string{
		"package/escape_code.yaml": "id: QmP66dBkc5zUMF7W7HuU3HgTmPa1w92gGNG4zY854f4Wkt\ntype: wasm\n" +
			"source: ../../../../etc\noutput: out\n" + steps("rust:1.83", "cat shadow"),
	})

	c, err := schema.New(schema.WithLocal(escaping), schema.WithBranch("main"))
	require.NoError(t, err)

	_, _, err = c.Compile(t.Context())
	require.ErrorContains(t, err, "climbs out of the repository")
}

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

func TestABranchWhoseDeploymentIsNotLive(t *testing.T) {
	// Only production is published; release/* feeds staging, which is not.
	r := deployed(t, perEnvironment(t), "production")

	w := work(mainRepo)
	w.Ref = "refs/heads/release/1.4"

	err := planned(t.Context(), builder(t, r, &checkout{dir: t.TempDir()}, &engine{}, mainRepo), w, newOut())
	require.ErrorIs(t, err, artifact.ErrNoDeployment)
	require.ErrorContains(t, err, "none of those is deployed")
}

func TestABranchThatFeedsNoDeployment(t *testing.T) {
	r := deployed(t, projectConfig(t, "environments:\n    production:\n        branches: [main]\n", nil), "production")

	w := work(mainRepo)
	w.Ref = "refs/heads/wip"

	err := planned(t.Context(), builder(t, r, &checkout{dir: t.TempDir()}, &engine{}, mainRepo), w, newOut())
	require.ErrorIs(t, err, artifact.ErrNoDeployment)
	require.ErrorContains(t, err, `"wip" feeds none of production`)
}

func TestAPackagesValuesAreItsBuildsEnvironment(t *testing.T) {
	config := projectConfig(t, everyBranch, map[string]string{
		"package/reserve_code.yaml": "id: QmbB2PpNzfnQ3wAJykt9UZU2SRrFNSzawU16yo9mdpM2Wh\ntype: wasm\n" +
			"source: services/reserve\noutput: out.wasm\n" +
			"variables:\n    GOPRIVATE:\n        value: github.com/acme/*\n" +
			steps("rust:1.83", "cargo build"),
		// a workload's values are its own, read when it starts, and never the build's
		"function/reserve.yaml": "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc\npackage:\n    name: reserve_code\n" +
			"execution:\n    call: reserve\n    timeout: 15s\n    memory: 32MB\n" +
			"variables:\n    API_BASE:\n        value: https://api.example.com\n",
	})

	e := &engine{}
	require.NoError(t, planned(t.Context(), builder(t, deployed(t, config), &checkout{dir: t.TempDir()}, e, mainRepo), work(mainRepo), newOut()))

	specs := e.seen()
	require.Len(t, specs, 1)
	require.Equal(t, map[string]string{"GOPRIVATE": "github.com/acme/*"}, specs[0].Variables)
}

// vaultOf records what a build's vault was asked, and answers.
type vaultOf struct {
	mu    sync.Mutex
	asked []string
	err   error
}

func (v *vaultOf) Secret(_ context.Context, project, environment, secretID string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.asked = append(v.asked, project+"/"+environment+"/"+secretID)
	if v.err != nil {
		return "", v.err
	}
	return "sk_live_1", nil
}

// answers is a vault holding values outright.
type answers map[string]string

func (a answers) Read(_ context.Context, label string) ([]byte, error) {
	value, ok := a[label]
	if !ok {
		return nil, vault.ErrNotGranted
	}
	return []byte(value), nil
}

const stripeID = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy"

// A secret is read by the step itself, through the helper, under the package's own
// grant; nothing resolves one into the build's environment.
func TestASecretIsNeverInTheBuildsEnvironment(t *testing.T) {
	e := &engine{}
	secrets := &vaultOf{}
	require.NoError(t, planned(t.Context(), builder(t, deployed(t, basicFixture), &checkout{dir: source(t)}, e, mainRepo, artifact.WithSecrets(secrets)), work(paylibRepo), newOut()))

	specs := e.seen()
	require.Len(t, specs, 1)
	require.NotContains(t, specs[0].Variables, "GITHUB_TOKEN")
	require.Equal(t, []string{"export GITHUB_TOKEN=$(/.taubyte/secret stripe) && .taubyte/build.sh"}, specs[0].Steps[0].Commands)
	require.Equal(t, []string{"stripe"}, specs[0].Secrets, "the grant, by the label the step reads it with")
	require.Empty(t, secrets.asked, "nothing is read until a step asks")
}

func TestTheBuildsVaultReadsWhatThePackageIsGranted(t *testing.T) {
	e := &engine{}
	secrets := &vaultOf{}
	require.NoError(t, planned(t.Context(), builder(t, deployed(t, basicFixture), &checkout{dir: source(t)}, e, mainRepo, artifact.WithSecrets(secrets)), work(paylibRepo), newOut()))
	v := e.seen()[0].Vault
	require.NotNil(t, v)

	value, err := v.Read(t.Context(), "stripe")
	require.NoError(t, err)
	require.Equal(t, "sk_live_1", string(value))
	require.Equal(t, []string{projectID + "/prod/" + stripeID}, secrets.asked, "by id, in the deployment built")

	// Another secret of the project is not this package's to read.
	_, err = v.Read(t.Context(), "slack")
	require.ErrorIs(t, err, vault.ErrNotGranted)
	require.Len(t, secrets.asked, 1, "a refused label never reaches the secrets service")

	secrets.err = errors.New("no member answered")
	_, err = v.Read(t.Context(), "stripe")
	require.ErrorContains(t, err, "no member answered")
}

func TestAGrantChangesTheRecipe(t *testing.T) {
	granted := artifact.Spec{Steps: []artifact.Step{{Name: "b", Image: "go:1.23"}}, Output: "x", Secrets: []string{"stripe"}}
	plain := granted
	plain.Secrets = nil
	require.NotEqual(t, granted.Digest(), plain.Digest())

	// The vault is who answers, not what is built.
	granted.Vault = answers{"stripe": "sk_live_1"}
	again := granted
	again.Vault = nil
	require.Equal(t, granted.Digest(), again.Digest())
}

func TestAGrantedPackageInACloudWithNoSecretsIsRefused(t *testing.T) {
	e := &engine{}
	err := planned(t.Context(), builder(t, deployed(t, basicFixture), &checkout{dir: source(t)}, e, mainRepo), work(paylibRepo), newOut())
	require.ErrorIs(t, err, artifact.ErrNoSecrets)
	require.ErrorContains(t, err, "package/paylib is granted stripe")
	require.Empty(t, e.seen())
}

func TestAnInputIsRefusedByName(t *testing.T) {
	config := projectConfig(t, everyBranch, map[string]string{
		"package/paylib.yaml": "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\ntype: wasm\n" +
			"source:\n    git: https://github.com/acme/paylib\noutput: paylib.wasm\n" + steps("go:1.23", "make"),
		"package/reserve_code.yaml": "id: QmbB2PpNzfnQ3wAJykt9UZU2SRrFNSzawU16yo9mdpM2Wh\ntype: assets\n" +
			"source: services/reserve\noutput: dist\n" +
			"build:\n    inputs:\n        pay:\n            name: paylib\n" +
			"    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n",
	})
	e := &engine{}
	err := planned(t.Context(), builder(t, deployed(t, config), &checkout{dir: source(t)}, e, mainRepo), work(mainRepo), newOut())
	require.ErrorIs(t, err, artifact.ErrNotBuiltHere)
	require.ErrorContains(t, err, "package/reserve_code reads other packages' artifacts as inputs")
	require.Empty(t, e.seen())
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

func TestAnUnreadDelegatedBlockIsRefusedByName(t *testing.T) {
	delegated := projectConfig(t, everyBranch, map[string]string{
		"package/paylib.yaml": "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\ntype: wasm\n" +
			"source:\n    git: https://github.com/acme/paylib\n    branch: main\n    config: taubyte.yaml\noutput: paylib.wasm\n" +
			steps("taubyte/go-wasi:v2", ".taubyte/build.sh"),
	})
	e := &engine{}

	err := planned(t.Context(), builder(t, deployed(t, delegated), &checkout{dir: source(t)}, e, mainRepo), work(paylibRepo), newOut())
	require.ErrorIs(t, err, artifact.ErrUnfilled)
	require.ErrorContains(t, err, `package/paylib names source.config "taubyte.yaml"`)
	require.Empty(t, e.seen())
}

func TestABarePathIsTheMainRepositoryNotACodeRepository(t *testing.T) {
	r := deployed(t, oneEnvironment(t))
	type testCase struct {
		pushed string
		images []string
	}
	for _, c := range []testCase{
		{mainRepo, []string{"rust:1.83"}},
		{"github.com/acme/shop-code", nil},
		{"github.com/acme/another-code", nil},
		{paylibRepo, []string{"taubyte/go-wasi:v2"}},
	} {
		e := &engine{}
		require.NoError(t, planned(t.Context(), builder(t, r, &checkout{dir: source(t)}, e, mainRepo), work(c.pushed), newOut()), c.pushed)
		var images []string
		for _, s := range e.seen() {
			images = append(images, imageOf(s))
		}
		require.Equal(t, c.images, images, c.pushed)
	}
}

func TestABuildTheEngineDeclinesIsReportedUnbuilt(t *testing.T) {
	e := &engine{err: fmt.Errorf("%w: this universe runs no containers", artifact.ErrNoEngine)}
	o := newOut()

	require.NoError(t, planned(t.Context(), builder(t, deployed(t, oneEnvironment(t)), &checkout{dir: source(t)}, e, mainRepo), work(mainRepo), o))
	require.NotEmpty(t, e.seen())
	require.Empty(t, o.stored())
	require.Contains(t, o.text(), "not built: package/reserve_code for prod")
	require.Contains(t, o.text(), "this universe runs no containers")
	require.Contains(t, o.text(), "(recipe ", "and every build it was asked for named its recipe")
}
