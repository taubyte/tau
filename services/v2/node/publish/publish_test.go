package publish_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/project"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/v2/node/publish"
)

// register is the real one over a map: what a publish does is decided by what
// is already there, so a fake that forgets would test nothing.
func register(t *testing.T) *pkgregister.Register {
	t.Helper()
	r, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	return r
}

// fixture is the vocabulary's own project, on a filesystem a compile can read.
func fixture(t *testing.T) (afero.Fs, string) {
	t.Helper()

	fs := afero.NewMemMapFs()
	src := "../../../../pkg/tcc/taubyte/v2/fixtures/basic"

	require.NoError(t, afero.Walk(afero.NewOsFs(), src, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return afero.WriteFile(fs, "/project"+path[len(src):], data, 0o644)
	}))
	return fs, "/project"
}

func options() publish.Options {
	return publish.Options{Project: "shop", Environment: "prod", Branch: "main"}
}

// TestADirectoryBecomesSomethingAFleetServes. The step nothing else did: tcc
// compiles, the register holds, a worker reads, and nothing joined them.
func TestADirectoryIsPublished(t *testing.T) {
	fs, dir := fixture(t)
	r := register(t)

	result, err := publish.Dir(t.Context(), r, fs, dir, options())
	require.NoError(t, err)

	require.NotEmpty(t, result.Object)
	require.Equal(t, uint64(1), result.Revision, "the first publish is revision one")
	require.False(t, result.Unchanged)

	// And what it published is what the register now holds.
	pointer, err := r.Resolve(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, result.Object, pointer.Object)

	compiled, err := r.Object(t.Context(), pointer.Object)
	require.NoError(t, err)

	p, err := project.Load(compiled)
	require.NoError(t, err)
	_, _, err = p.Resource(nil, "function", "checkout")
	require.NoError(t, err, "a reader gets back a project it can read")
}

// TestARevisionIsDerivedAndNotTyped. The register refuses one that does not
// move forward, which is a property worth having and not a number anybody
// should be tracking.
func TestTheRevisionIsDerived(t *testing.T) {
	fs, dir := fixture(t)
	r := register(t)

	first, err := publish.Dir(t.Context(), r, fs, dir, options())
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.Revision)

	// A change to the project itself, so it compiles to a different object.
	require.NoError(t, bump(fs, dir))

	second, err := publish.Dir(t.Context(), r, fs, dir, options())
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.Revision)
	require.NotEqual(t, first.Object, second.Object)

	// Somebody who wants to say which revision still can.
	explicit := options()
	explicit.Revision = 10
	third, err := publish.Dir(t.Context(), r, fs, dir, explicit)
	require.NoError(t, err)
	require.Equal(t, uint64(10), third.Revision)
}

// TestPublishingTheSameThingTwiceChangesNothing. A compiled project is
// content-addressed, so bumping the revision for an unchanged directory would
// churn every reader's pointer to say the same thing.
func TestAnUnchangedDirectoryDoesNotMoveThePointer(t *testing.T) {
	fs, dir := fixture(t)
	r := register(t)

	first, err := publish.Dir(t.Context(), r, fs, dir, options())
	require.NoError(t, err)

	again, err := publish.Dir(t.Context(), r, fs, dir, options())
	require.NoError(t, err)

	require.True(t, again.Unchanged)
	require.Equal(t, first.Object, again.Object)
	require.Equal(t, first.Revision, again.Revision)

	pointer, err := r.Resolve(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, first.Revision, pointer.Revision, "the pointer did not move")
}

// TestTheCloudCompletesTheNamesTheProjectCannotKnow. A generated host is
// `<label>.<project label>` in the config, and the rest belongs to whoever runs
// the cloud.
func TestGeneratedNamesAreCompleted(t *testing.T) {
	fs, dir := fixture(t)
	r := register(t)

	o := options()
	o.CloudFqdn = "tau.example.io"

	result, err := publish.Dir(t.Context(), r, fs, dir, o)
	require.NoError(t, err)
	require.Empty(t, result.Incomplete)

	var generated bool
	for _, host := range result.Hosts {
		if len(host) > len("tau.example.io") && host[len(host)-len("tau.example.io"):] == "tau.example.io" {
			generated = true
		}
	}
	require.True(t, generated, "no generated name was completed: %v", result.Hosts)

	// And every published name answers.
	for _, host := range result.Hosts {
		project, environment, err := r.ResolveHost(t.Context(), host)
		require.NoError(t, err, host)
		require.Equal(t, "shop", project)
		require.Equal(t, "prod", environment)
	}
}

// TestANameThatCannotBeCompletedIsNamedAndNotPublished. A partial name in a
// routing table is a name no request will ever carry.
func TestAnIncompleteNameIsReported(t *testing.T) {
	fs, dir := fixture(t)
	r := register(t)

	result, err := publish.Dir(t.Context(), r, fs, dir, options())
	require.NoError(t, err)

	require.NotEmpty(t, result.Incomplete, "the fixture has generated names")
	for _, name := range result.Incomplete {
		require.NotContains(t, result.Hosts, name)

		_, _, err := r.ResolveHost(t.Context(), name)
		require.ErrorIs(t, err, pkgregister.ErrNoHost, "a partial name was published: %s", name)
	}

	// The names the project's own zones complete are published regardless.
	require.Contains(t, result.Hosts, "shop.example.com")
}

// TestANameSomebodyElseHoldsFailsThePublish, rather than taking half of them.
func TestAClaimedNameFailsThePublish(t *testing.T) {
	fs, dir := fixture(t)
	r := register(t)

	_, err := r.Publish(t.Context(), pkgregister.Publication{
		Project: "impostor", Environment: "prod", Revision: 1,
		Compiled: []byte("x"), Hosts: []string{"shop.example.com"},
	})
	require.NoError(t, err)

	_, err = publish.Dir(t.Context(), r, fs, dir, options())
	require.ErrorIs(t, err, pkgregister.ErrHostClaimed)

	_, err = r.Resolve(t.Context(), "shop", "prod")
	require.ErrorIs(t, err, pkgregister.ErrNotPublished, "nothing was published")
}

func TestWhatItRefuses(t *testing.T) {
	fs, dir := fixture(t)
	r := register(t)

	bare := publish.Options{Environment: "prod"}
	_, err := publish.Dir(t.Context(), r, fs, dir, bare)
	require.ErrorIs(t, err, publish.ErrNoProject)

	_, err = publish.Dir(t.Context(), nil, fs, dir, options())
	require.ErrorIs(t, err, publish.ErrNoRegister)

	_, err = publish.Dir(t.Context(), r, afero.NewMemMapFs(), "/nothing-here", options())
	require.Error(t, err, "an empty directory is not a project")
}

// TestAnUnreachableRegisterIsReportedBeforeAnythingIsWritten.
func TestAnUnreachableRegister(t *testing.T) {
	fs, dir := fixture(t)

	_, err := publish.Dir(t.Context(), broken{}, fs, dir, options())
	require.Error(t, err)
	require.Contains(t, err.Error(), "shop/prod")
}

type broken struct{}

func (broken) Resolve(context.Context, string, string) (pkgregister.Pointer, error) {
	return pkgregister.Pointer{}, errors.New("the register is unreachable")
}

func (broken) Publish(context.Context, pkgregister.Publication) (pkgregister.ObjectID, error) {
	return "", errors.New("the register is unreachable")
}

// bump changes one value so the directory compiles to a different object.
func bump(fs afero.Fs, dir string) error {
	data, err := afero.ReadFile(fs, dir+"/project.yaml")
	if err != nil {
		return err
	}

	changed := strings.Replace(string(data),
		"description: minimal v2 project", "description: changed", 1)
	if changed == string(data) {
		return errors.New("the fixture's project.yaml no longer says what this test edits")
	}
	return afero.WriteFile(fs, dir+"/project.yaml", []byte(changed), 0o644)
}
