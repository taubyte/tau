package deploy_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/run"
)

const (
	basicFixture = "../../../pkg/tcc/taubyte/v2/fixtures/basic"
	envsFixture  = "../../../pkg/tcc/taubyte/v2/fixtures/environments"
)

// projects answers the one question a deploy asks of auth.
type projects struct {
	config string
	err    error
}

func (p projects) Configures(_ context.Context, _, repositoryKey string) (bool, error) {
	return repositoryKey == p.config, p.err
}

// domains proves the zones it holds, for any project, and counts the asks.
type domains struct {
	proven map[string]bool
	asked  int
}

func (d *domains) Prove(_ context.Context, _, fqdn string) error {
	d.asked++
	if d.proven[fqdn] {
		return nil
	}
	return errors.New("no TXT record")
}

func provenAll() *domains {
	return &domains{proven: map[string]bool{"example.com": true, "moved.example": true}}
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

// fixture copies a config tree onto disk, because that is what a checkout hands
// back — a directory on this machine, for the same reason a build needs one.
func fixture(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	copyInto(t, src, dst)
	return dst
}

func copyInto(t *testing.T, src, dst string) {
	t.Helper()
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
}

func registry(t *testing.T) *register.Register {
	t.Helper()
	r, err := register.New(register.WithStore(register.NewMemStore()))
	require.NoError(t, err)
	return r
}

// shopID is the id every fixture's project.yaml carries: the CID of "shop".
// The work a push becomes names the same project, because the repository was
// linked under that id.
const shopID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"

// envsID is the envs fixture's own project id.
const envsID = "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG"

func envsWork() run.Work {
	w := work()
	w.ProjectID = envsID
	return w
}

// pushed is a push as build runs it: the plan, then one job per environment
// it names, each told its own.
func pushed(ctx context.Context, d *deploy.Deploy, w run.Work, out run.Output) error {
	environments, err := d.Plan(ctx, w, out)
	if err != nil {
		return err
	}
	for _, environment := range environments {
		w.Environment = environment
		if err := d.Run(ctx, w, out); err != nil {
			return err
		}
	}
	return nil
}

func work() run.Work {
	return run.Work{
		JobID:         "job",
		RepositoryKey: "github.com/acme/shop-config",
		Ref:           "refs/heads/main",
		CommitSHA:     "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c",
		ProjectID:     shopID,
		Attempt:       1,
	}
}

func runner(t *testing.T, c *checkout, r deploy.Register, opts ...deploy.Option) *deploy.Deploy {
	t.Helper()

	base := []deploy.Option{
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(c),
		deploy.WithLocalRegister(r),
		// The fixture is written for a cloud with region-1 and region-2; these deploys are region-1.
		deploy.WithRegions(schema.WithRegion("region-1")),
		deploy.WithDomains(provenAll()),
	}
	d, err := deploy.New(append(base, opts...)...)
	require.NoError(t, err)
	return d
}

func TestAPushPublishes(t *testing.T) {
	c := &checkout{dir: fixture(t, basicFixture)}
	r := registry(t)

	var log out
	require.NoError(t, pushed(t.Context(), runner(t, c, r), work(), &log))

	pointer, err := r.Resolve(t.Context(), shopID, "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(1), pointer.Revision)
	require.NotEmpty(t, pointer.Object)

	// The developer watching sees which deployments the branch fed and what
	// each one now answers on.
	require.Contains(t, log.String(), "main feeds "+"prod")
	require.Contains(t, log.String(), "published as revision 1")

	// The clone is released whatever happened.
	require.True(t, c.released)
}

func TestTheBranchDecidesTheDeployments(t *testing.T) {
	c := &checkout{dir: fixture(t, envsFixture)}
	r := registry(t)

	var log out
	require.NoError(t, pushed(t.Context(), runner(t, c, r), envsWork(), &log))

	for _, name := range []string{"production", "ab", "preview"} {
		pointer, err := r.Resolve(t.Context(), envsID, name)
		require.NoError(t, err, name)
		require.Equal(t, uint64(1), pointer.Revision, name)
	}

	_, err := r.Resolve(t.Context(), envsID, "staging")
	require.ErrorIs(t, err, register.ErrNotPublished, "release/* is staging's, and main is not")

	// A release branch feeds the other set.
	w := envsWork()
	w.Ref = "refs/heads/release/1.4"
	require.NoError(t, pushed(t.Context(), runner(t, c, r), w, &log))

	pointer, err := r.Resolve(t.Context(), envsID, "staging")
	require.NoError(t, err)
	require.Equal(t, uint64(1), pointer.Revision)
}

func TestEachDeploymentIsItsOwnObject(t *testing.T) {
	dir := fixture(t, envsFixture)
	r := registry(t)

	var log out
	require.NoError(t, pushed(t.Context(), runner(t, &checkout{dir: dir}, r), envsWork(), &log))

	production, err := r.Resolve(t.Context(), envsID, "production")
	require.NoError(t, err)
	preview, err := r.Resolve(t.Context(), envsID, "preview")
	require.NoError(t, err)

	require.NotEqual(t, production.Object, preview.Object,
		"preview's timeout is 5s and production's is 30s; one object for both would give preview production's numbers")
}

func TestAnUnchangedPushDoesNotMoveThePointer(t *testing.T) {
	c := &checkout{dir: fixture(t, basicFixture)}
	r := registry(t)

	var log out
	require.NoError(t, pushed(t.Context(), runner(t, c, r), work(), &log))
	require.NoError(t, pushed(t.Context(), runner(t, c, r), work(), &log))

	pointer, err := r.Resolve(t.Context(), shopID, "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(1), pointer.Revision)
	require.Contains(t, log.String(), "unchanged at revision 1")
}

func TestAGeneratedNameNeedsTheCloudsZone(t *testing.T) {
	dir := fixture(t, basicFixture)
	r := registry(t)

	var without out
	require.NoError(t, pushed(t.Context(), runner(t, &checkout{dir: dir}, r), work(), &without))
	require.Contains(t, without.String(), "<cloud fqdn>")

	told := registry(t)
	var with out
	require.NoError(t, pushed(t.Context(), runner(t, &checkout{dir: dir}, told,
		deploy.WithCloudFqdn("acme.example")), work(), &with))

	require.Contains(t, with.String(), "acme.example")
	require.NotContains(t, with.String(), "<cloud fqdn>")

	// And the completed name is what the register claims, so a gateway
	// resolving a Host header finds it.
	project, environment, err := told.ResolveHost(t.Context(), "storefront.tdw4ygm23gwgn.acme.example")
	require.NoError(t, err)
	require.Equal(t, shopID, project)
	require.Equal(t, "prod", environment)
}

func TestACodeRepositoryIsRefusedByName(t *testing.T) {
	dir := fixture(t, basicFixture)

	w := work()
	w.RepositoryKey = "github.com/acme/shop-code"

	err := pushed(t.Context(), runner(t, &checkout{dir: dir}, registry(t)), w, &out{})
	require.ErrorIs(t, err, deploy.ErrNotConfiguration)
}

func TestWhatElseItRefuses(t *testing.T) {
	dir := fixture(t, basicFixture)
	r := registry(t)

	// A tag is not a deployment: nothing in an environment table matches one.
	w := work()
	w.Ref = "refs/tags/v1.0.0"
	err := pushed(t.Context(), runner(t, &checkout{dir: dir}, r), w, &out{})
	require.ErrorIs(t, err, deploy.ErrNotABranch)

	// A branch no deployment claims, on a project whose table claims some.
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
	err = pushed(t.Context(), runner(t, &checkout{dir: narrow}, r), w, &out{})
	require.ErrorIs(t, err, deploy.ErrNoDeployment)
	require.ErrorContains(t, err, "production")

	// A checkout that could not read the repository.
	err = pushed(t.Context(), runner(t, &checkout{err: errors.New("no such ref")}, r), work(), &out{})
	require.ErrorContains(t, err, "no such ref")
	require.ErrorContains(t, err, "0f1e2d3c", "the commit is named, so a rewritten branch is diagnosable")
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

func TestConfigThatDoesNotCompile(t *testing.T) {
	broken := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(broken, "project.yaml"),
		[]byte("id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nname: broken\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(broken, "zone"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(broken, "zone", "bad.yaml"),
		[]byte("id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Za\nnot_a_key: 1\n"), 0o644))

	err := pushed(t.Context(), runner(t, &checkout{dir: broken}, registry(t)), work(), &out{})
	require.ErrorContains(t, err, "not_a_key")

	// A directory with no project at all.
	err = pushed(t.Context(), runner(t, &checkout{dir: filepath.Join(t.TempDir(), "nothing")}, registry(t)),
		work(), &out{})
	require.Error(t, err)
}

func TestAuthFailingIsNotARefusal(t *testing.T) {
	dir := fixture(t, basicFixture)

	d, err := deploy.New(
		deploy.WithRegions(schema.WithRegion("region-1")),
		deploy.WithDomains(provenAll()),
		deploy.WithProjects(projects{config: "github.com/acme/shop-config", err: errors.New("auth is down")}),
		deploy.WithCheckout(&checkout{dir: dir}),
		deploy.WithLocalRegister(registry(t)),
	)
	require.NoError(t, err)

	err = pushed(t.Context(), d, work(), &out{})
	require.ErrorContains(t, err, "auth is down")
	require.NotErrorIs(t, err, deploy.ErrNotConfiguration)
}

func TestNoRegisterFailsAfterTheCompile(t *testing.T) {
	c := &checkout{dir: fixture(t, basicFixture)}

	d, err := deploy.New(
		deploy.WithRegions(schema.WithRegion("region-1")),
		deploy.WithDomains(provenAll()),
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(c),
		deploy.WithRegister(func(context.Context) (deploy.Register, error) {
			return nil, errors.New("no register is reachable")
		}),
	)
	require.NoError(t, err)

	var log out
	err = pushed(t.Context(), d, work(), &log)
	require.ErrorContains(t, err, "no register is reachable")
	require.Contains(t, log.String(), "compiled ", "the compile ran and said so before the fleet was needed")
}

func TestAPublishThatFails(t *testing.T) {
	dir := fixture(t, basicFixture)

	d, err := deploy.New(
		deploy.WithRegions(schema.WithRegion("region-1")),
		deploy.WithDomains(provenAll()),
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(&checkout{dir: dir}),
		deploy.WithLocalRegister(refusing{}),
	)
	require.NoError(t, err)

	require.ErrorContains(t, pushed(t.Context(), d, work(), &out{}), "refused")

	// And a register that cannot even be read.
	d, err = deploy.New(
		deploy.WithRegions(schema.WithRegion("region-1")),
		deploy.WithDomains(provenAll()),
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(&checkout{dir: dir}),
		deploy.WithLocalRegister(unreadable{}),
	)
	require.NoError(t, err)
	require.ErrorContains(t, pushed(t.Context(), d, work(), &out{}), "unreadable")
}

type refusing struct{}

func (refusing) Resolve(context.Context, string, string) (register.Pointer, error) {
	return register.Pointer{}, register.ErrNotPublished
}
func (refusing) Manifest(context.Context, string, string) (register.Manifest, register.ObjectID, error) {
	return register.Manifest{}, "", register.ErrNotPublished
}
func (refusing) Publish(context.Context, register.Publication) (register.ObjectID, error) {
	return "", errors.New("refused")
}

type unreadable struct{}

func (unreadable) Resolve(context.Context, string, string) (register.Pointer, error) {
	return register.Pointer{}, errors.New("unreadable")
}
func (unreadable) Manifest(context.Context, string, string) (register.Manifest, register.ObjectID, error) {
	return register.Manifest{}, "", errors.New("unreadable")
}
func (unreadable) Publish(context.Context, register.Publication) (register.ObjectID, error) {
	return "", nil
}

func TestACodePushIsDelegated(t *testing.T) {
	dir := fixture(t, basicFixture)
	b := &builderStub{}

	d, err := deploy.New(
		deploy.WithRegions(schema.WithRegion("region-1")),
		deploy.WithDomains(provenAll()),
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(&checkout{dir: dir}),
		deploy.WithLocalRegister(registry(t)),
		deploy.WithBuilder(b),
	)
	require.NoError(t, err)

	w := work()
	w.RepositoryKey = "github.com/acme/shop-code"

	var log out
	require.NoError(t, pushed(t.Context(), d, w, &log))
	require.Equal(t, 1, b.calls, "the code push went to the builder")
	require.Equal(t, w.RepositoryKey, b.saw.RepositoryKey)
	require.Contains(t, log.String(), "delegated")

	// A CONFIGURATION PUSH PUBLISHES, THEN BUILDS: a bare path is in the
	// configuration repository, so the builder is handed the same push once
	// the deployments it reads are published.
	b.calls = 0
	log = out{}
	require.NoError(t, pushed(t.Context(), d, work(), &log))
	require.Equal(t, 1, b.calls)
	require.Equal(t, work().RepositoryKey, b.saw.RepositoryKey)
	require.Less(t, strings.Index(log.String(), "published"), strings.Index(log.String(), "delegated"), "published before it built")
}

func TestACodePushThatFails(t *testing.T) {
	d, err := deploy.New(
		deploy.WithRegions(schema.WithRegion("region-1")),
		deploy.WithDomains(provenAll()),
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(&checkout{dir: fixture(t, basicFixture)}),
		deploy.WithLocalRegister(registry(t)),
		deploy.WithBuilder(&builderStub{err: errors.New("the toolchain is not installed")}),
	)
	require.NoError(t, err)

	w := work()
	w.RepositoryKey = "github.com/acme/shop-code"
	require.ErrorContains(t, pushed(t.Context(), d, w, &out{}), "the toolchain is not installed")
}

func TestWithBuilderRefusesNil(t *testing.T) {
	_, err := deploy.New(deploy.WithBuilder(nil))
	require.ErrorContains(t, err, "builder")
}

type builderStub struct {
	calls int
	saw   run.Work
	err   error
}

// Plan is the deployments a code push feeds, as the builder reads them from what the
// project publishes.
func (b *builderStub) Plan(_ context.Context, w run.Work, out io.Writer) ([]string, error) {
	fmt.Fprintln(out, "planned")
	return []string{"prod"}, nil
}

func (b *builderStub) Build(_ context.Context, w run.Work, out run.Output) error {
	b.calls++
	b.saw = w
	if b.err != nil {
		return b.err
	}
	fmt.Fprintln(out, "delegated")
	return nil
}

func TestAConfigNamingAnIdItDidNotClaimPublishesNothing(t *testing.T) {
	c := &checkout{dir: fixture(t, basicFixture)}
	r := registry(t)

	w := work()
	w.ProjectID = "QmSomebodyElsesProject"
	err := pushed(t.Context(), runner(t, c, r, deploy.WithProjects(projects{config: w.RepositoryKey})), w, &out{})
	require.ErrorIs(t, err, deploy.ErrNotClaimed)
	require.Contains(t, err.Error(), shopID)

	_, err = r.Resolve(t.Context(), shopID, "prod")
	require.ErrorIs(t, err, register.ErrNotPublished)
	_, err = r.Resolve(t.Context(), w.ProjectID, "prod")
	require.ErrorIs(t, err, register.ErrNotPublished)
}

func TestAnUnprovenZoneIsRefusedBeforeAnythingPublishes(t *testing.T) {
	r := registry(t)
	none := &domains{}
	err := pushed(t.Context(), runner(t, &checkout{dir: fixture(t, basicFixture)}, r, deploy.WithDomains(none)), work(), &out{})
	require.ErrorIs(t, err, deploy.ErrUnproven)
	require.ErrorContains(t, err, "example.com: no TXT record")
	require.ErrorContains(t, err, "tau domain claim example.com")
	_, err = r.Resolve(t.Context(), shopID, "prod")
	require.ErrorIs(t, err, register.ErrNotPublished)

	d, err := deploy.New(
		deploy.WithRegions(schema.WithRegion("region-1")),
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(&checkout{dir: fixture(t, basicFixture)}),
		deploy.WithLocalRegister(r),
	)
	require.NoError(t, err)
	err = pushed(t.Context(), d, work(), &out{})
	require.ErrorIs(t, err, deploy.ErrUnproven)
	require.ErrorContains(t, err, "this cloud proves no domain")

	_, err = deploy.New(deploy.WithDomains(nil))
	require.ErrorContains(t, err, "domains must not be nil")
}

func TestAProvenZonePublishes(t *testing.T) {
	proven := provenAll()
	require.NoError(t, pushed(t.Context(), runner(t, &checkout{dir: fixture(t, basicFixture)}, registry(t), deploy.WithDomains(proven)), work(), &out{}))
	require.Equal(t, 1, proven.asked)
}

func TestWhatAPushDeploysIsCompiledPerEnvironmentItFeeds(t *testing.T) {
	projects, err := deploy.Compiled(fixture(t, basicFixture), "main")
	require.NoError(t, err)
	require.Contains(t, projects, "prod")
	require.NotNil(t, projects["prod"])

	_, err = deploy.Compiled(t.TempDir(), "main")
	require.Error(t, err)
}
