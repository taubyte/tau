package deploy_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/deploy"
)

const (
	basicFixture = "../../../pkg/tcc/taubyte/v2/fixtures/basic"
	envsFixture  = "../../../pkg/tcc/taubyte/v2/fixtures/environments"
)

// ── the seams, stood up ──────────────────────────────────────────────────

// projects answers the one question a deploy asks of auth.
type projects struct {
	config string
	err    error
}

func (p projects) Configures(_ context.Context, _, repositoryKey string) (bool, error) {
	return repositoryKey == p.config, p.err
}

// checkout hands over a fixture instead of cloning. The clone is tested
// separately, against a real repository; what this stands in for is the network.
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

// out collects the build log a developer watches.
type out struct{ strings.Builder }

func (o *out) Asset(context.Context, string, io.Reader) error { return nil }

// backend.Output's Asset takes an io.Reader; this deploy produces no assets, so
// it is here only to satisfy the interface.
type output struct{ *out }

func (output) Asset(context.Context, string, ioReader) error { return nil }

type ioReader = interface {
	Read(p []byte) (n int, err error)
}

// fixture copies a config tree onto disk, because that is what a checkout hands
// back — a directory on this machine, for the same reason a build needs one.
func fixture(t *testing.T, src string) string {
	t.Helper()

	dst := t.TempDir()
	require.NoError(t, filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, strings.TrimPrefix(path, src))
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}))
	return dst
}

func registry(t *testing.T) *register.Register {
	t.Helper()
	r, err := register.New(register.WithStore(register.NewMemStore()))
	require.NoError(t, err)
	return r
}

func work() backend.Work {
	return backend.Work{
		JobID:         "job",
		RepositoryKey: "github.com/acme/shop-config",
		Ref:           "refs/heads/main",
		CommitSHA:     "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c",
		ProjectID:     "shop",
		Attempt:       1,
	}
}

func runner(t *testing.T, c *checkout, r deploy.Register, opts ...deploy.Option) *deploy.Deploy {
	t.Helper()

	base := []deploy.Option{
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(c),
		deploy.WithLocalRegister(r),
	}
	d, err := deploy.New(append(base, opts...)...)
	require.NoError(t, err)
	return d
}

// ── what a push does ─────────────────────────────────────────────────────

// A PUSH TO THE CONFIG REPOSITORY IS THE DEPLOY. Nothing else writes the
// register, and nobody typed an environment or a revision.
func TestAPushPublishes(t *testing.T) {
	c := &checkout{dir: fixture(t, basicFixture)}
	r := registry(t)

	var log out
	require.NoError(t, runner(t, c, r).Run(t.Context(), work(), &log))

	pointer, err := r.Resolve(t.Context(), "shop", deploy.DefaultEnvironment)
	require.NoError(t, err)
	require.Equal(t, uint64(1), pointer.Revision)
	require.NotEmpty(t, pointer.Object)

	// The developer watching sees which deployments the branch fed and what
	// each one now answers on.
	require.Contains(t, log.String(), "main feeds "+deploy.DefaultEnvironment)
	require.Contains(t, log.String(), "published as revision 1")

	// The clone is released whatever happened.
	require.True(t, c.released)
}

// THE BRANCH IS THE DEPLOYMENT. One push to `main` puts production and the
// shadow written inside it live; `release/*` is staging's, and neither is typed
// anywhere.
func TestTheBranchDecidesTheDeployments(t *testing.T) {
	c := &checkout{dir: fixture(t, envsFixture)}
	r := registry(t)

	var log out
	require.NoError(t, runner(t, c, r).Run(t.Context(), work(), &log))

	for _, name := range []string{"production", "ab", "preview"} {
		pointer, err := r.Resolve(t.Context(), "shop", name)
		require.NoError(t, err, name)
		require.Equal(t, uint64(1), pointer.Revision, name)
	}

	_, err := r.Resolve(t.Context(), "shop", "staging")
	require.ErrorIs(t, err, register.ErrNotPublished, "release/* is staging's, and main is not")

	// A release branch feeds the other set.
	w := work()
	w.Ref = "refs/heads/release/1.4"
	require.NoError(t, runner(t, c, r).Run(t.Context(), w, &log))

	pointer, err := r.Resolve(t.Context(), "shop", "staging")
	require.NoError(t, err)
	require.Equal(t, uint64(1), pointer.Revision)
}

// EACH DEPLOYMENT IS ITS OWN COMPILE, which is the whole point of a
// per-environment limit. Two deployments of one commit are two objects.
func TestEachDeploymentIsItsOwnObject(t *testing.T) {
	dir := fixture(t, envsFixture)
	r := registry(t)

	var log out
	require.NoError(t, runner(t, &checkout{dir: dir}, r).Run(t.Context(), work(), &log))

	production, err := r.Resolve(t.Context(), "shop", "production")
	require.NoError(t, err)
	preview, err := r.Resolve(t.Context(), "shop", "preview")
	require.NoError(t, err)

	require.NotEqual(t, production.Object, preview.Object,
		"preview's timeout is 5s and production's is 30s; one object for both would give preview production's numbers")
}

// A PUSH THAT CHANGED NOTHING THIS DEPLOYMENT READS MOVES NOTHING. A compiled
// project is content-addressed, so bumping the revision would churn every
// reader's pointer to say the same thing.
func TestAnUnchangedPushDoesNotMoveThePointer(t *testing.T) {
	c := &checkout{dir: fixture(t, basicFixture)}
	r := registry(t)

	var log out
	require.NoError(t, runner(t, c, r).Run(t.Context(), work(), &log))
	require.NoError(t, runner(t, c, r).Run(t.Context(), work(), &log))

	pointer, err := r.Resolve(t.Context(), "shop", deploy.DefaultEnvironment)
	require.NoError(t, err)
	require.Equal(t, uint64(1), pointer.Revision)
	require.Contains(t, log.String(), "unchanged at revision 1")
}

// THE CLOUD'S HALF OF A GENERATED NAME IS NOT IN THE CONFIG. Without it the
// name is reported rather than published: half a name in a routing table is one
// no request will ever carry.
func TestAGeneratedNameNeedsTheCloudsZone(t *testing.T) {
	dir := fixture(t, basicFixture)
	r := registry(t)

	var without out
	require.NoError(t, runner(t, &checkout{dir: dir}, r).Run(t.Context(), work(), &without))
	require.Contains(t, without.String(), "<cloud fqdn>")

	told := registry(t)
	var with out
	require.NoError(t, runner(t, &checkout{dir: dir}, told,
		deploy.WithCloudFqdn("acme.example")).Run(t.Context(), work(), &with))

	require.Contains(t, with.String(), "acme.example")
	require.NotContains(t, with.String(), "<cloud fqdn>")

	// And the completed name is what the register claims, so a gateway
	// resolving a Host header finds it.
	project, environment, err := told.ResolveHost(t.Context(), "storefront.tdw4ygm23gwgn.acme.example")
	require.NoError(t, err)
	require.Equal(t, "shop", project)
	require.Equal(t, deploy.DefaultEnvironment, environment)
}

// ── what it refuses ──────────────────────────────────────────────────────

// A CODE BUILD IS NAMED, NOT PASSED. One that returned success having produced
// nothing would look exactly like a deploy that worked.
func TestACodeRepositoryIsRefusedByName(t *testing.T) {
	dir := fixture(t, basicFixture)

	w := work()
	w.RepositoryKey = "github.com/acme/shop-code"

	err := runner(t, &checkout{dir: dir}, registry(t)).Run(t.Context(), w, &out{})
	require.ErrorIs(t, err, deploy.ErrNotConfiguration)
}

func TestWhatElseItRefuses(t *testing.T) {
	dir := fixture(t, basicFixture)
	r := registry(t)

	// A tag is not a deployment: nothing in an environment table matches one.
	w := work()
	w.Ref = "refs/tags/v1.0.0"
	err := runner(t, &checkout{dir: dir}, r).Run(t.Context(), w, &out{})
	require.ErrorIs(t, err, deploy.ErrNotABranch)

	// A branch no deployment claims, on a project whose table claims some.
	strict := &checkout{dir: fixture(t, envsFixture)}

	// Every branch is a preview in that fixture, so the case needs a table
	// with no catch-all. This is the message an author sees when they push a
	// branch nothing is wired to.
	narrow := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(narrow, "project.yaml"), []byte(
		"id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\n"+
			"name: narrow\n"+
			"environments:\n    production:\n        branches: [main]\n"), 0o644))

	w = work()
	w.Ref = "refs/heads/wip"
	err = runner(t, &checkout{dir: narrow}, r).Run(t.Context(), w, &out{})
	require.ErrorIs(t, err, deploy.ErrNoDeployment)
	require.ErrorContains(t, err, "production")

	// A checkout that could not read the repository.
	err = runner(t, &checkout{err: errors.New("no such ref")}, r).Run(t.Context(), work(), &out{})
	require.ErrorContains(t, err, "no such ref")
	require.ErrorContains(t, err, "0f1e2d3c", "the commit is named, so a rewritten branch is diagnosable")

	_ = strict
}

func TestWhatItCannotBeBuiltWithout(t *testing.T) {
	_, err := deploy.New()
	require.ErrorIs(t, err, deploy.ErrNoProjects)

	_, err = deploy.New(deploy.WithProjects(projects{}))
	require.ErrorIs(t, err, deploy.ErrNoCheckout)

	_, err = deploy.New(deploy.WithProjects(projects{}), deploy.WithCheckout(&checkout{}))
	require.ErrorIs(t, err, deploy.ErrNoRegister)

	_, err = deploy.New(deploy.WithProjects(nil))
	require.ErrorContains(t, err, "projects")

	_, err = deploy.New(deploy.WithCheckout(nil))
	require.ErrorContains(t, err, "checkout")

	_, err = deploy.New(deploy.WithRegister(nil))
	require.ErrorContains(t, err, "register")

	_, err = deploy.New(deploy.WithLocalRegister(nil))
	require.ErrorContains(t, err, "register")
}

// ── the paths a real cloud takes and a happy test does not ───────────────

// A CONFIG THAT DOES NOT COMPILE IS THE RUNNER'S ERROR. The queue's retry
// exists for a worker that died; retrying a compile error is waste, and the
// author needs the message rather than five copies of it.
func TestConfigThatDoesNotCompile(t *testing.T) {
	broken := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(broken, "project.yaml"),
		[]byte("id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nname: broken\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(broken, "zone"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(broken, "zone", "bad.yaml"),
		[]byte("id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Za\nnot_a_key: 1\n"), 0o644))

	err := runner(t, &checkout{dir: broken}, registry(t)).Run(t.Context(), work(), &out{})
	require.ErrorContains(t, err, "not_a_key")

	// A directory with no project at all.
	err = runner(t, &checkout{dir: filepath.Join(t.TempDir(), "nothing")}, registry(t)).
		Run(t.Context(), work(), &out{})
	require.Error(t, err)
}

// AUTH FAILING IS NOT "THIS IS A CODE REPOSITORY". One says the deploy cannot
// be decided; the other says it was decided against.
func TestAuthFailingIsNotARefusal(t *testing.T) {
	dir := fixture(t, basicFixture)

	d, err := deploy.New(
		deploy.WithProjects(projects{config: "github.com/acme/shop-config", err: errors.New("auth is down")}),
		deploy.WithCheckout(&checkout{dir: dir}),
		deploy.WithLocalRegister(registry(t)),
	)
	require.NoError(t, err)

	err = d.Run(t.Context(), work(), &out{})
	require.ErrorContains(t, err, "auth is down")
	require.NotErrorIs(t, err, deploy.ErrNotConfiguration)
}

// A REGISTER THAT CANNOT BE FOUND FAILS THE JOB, and it fails it AFTER every
// deployment compiled — so an author whose config is broken hears about the
// config rather than about the fleet.
func TestNoRegisterFailsAfterTheCompile(t *testing.T) {
	c := &checkout{dir: fixture(t, basicFixture)}

	d, err := deploy.New(
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(c),
		deploy.WithRegister(func(context.Context) (deploy.Register, error) {
			return nil, errors.New("no register is reachable")
		}),
	)
	require.NoError(t, err)

	var log out
	err = d.Run(t.Context(), work(), &log)
	require.ErrorContains(t, err, "no register is reachable")
	require.Contains(t, log.String(), "compiled ", "the compile ran and said so before the fleet was needed")
}

// A PUBLISH THAT FAILS FAILS THE JOB. A deploy that reported success having
// published nothing is the failure mode this whole path exists to avoid.
func TestAPublishThatFails(t *testing.T) {
	dir := fixture(t, basicFixture)

	d, err := deploy.New(
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(&checkout{dir: dir}),
		deploy.WithLocalRegister(refusing{}),
	)
	require.NoError(t, err)

	require.ErrorContains(t, d.Run(t.Context(), work(), &out{}), "refused")

	// And a register that cannot even be read.
	d, err = deploy.New(
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(&checkout{dir: dir}),
		deploy.WithLocalRegister(unreadable{}),
	)
	require.NoError(t, err)
	require.ErrorContains(t, d.Run(t.Context(), work(), &out{}), "unreadable")
}

type refusing struct{}

func (refusing) Resolve(context.Context, string, string) (register.Pointer, error) {
	return register.Pointer{}, register.ErrNotPublished
}
func (refusing) Publish(context.Context, register.Publication) (register.ObjectID, error) {
	return "", errors.New("refused")
}

type unreadable struct{}

func (unreadable) Resolve(context.Context, string, string) (register.Pointer, error) {
	return register.Pointer{}, errors.New("unreadable")
}
func (unreadable) Publish(context.Context, register.Publication) (register.ObjectID, error) {
	return "", nil
}
