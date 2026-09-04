package publish_test

import (
	"os"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/publish"
)

// envFixture declares three deployments, a shadow inside one, and suffixes keys
// for four names.
func envFixture(t *testing.T) (afero.Fs, string) {
	t.Helper()

	fs := afero.NewMemMapFs()
	src := "../tcc/taubyte/v2/fixtures/environments"

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

// THE DEFECT THIS TEST EXISTS FOR: a publish never told the compiler which
// environment it was for, nor which the project declared — so one document
// carrying `timeout_preview` beside `timeout` was refused as a typo, and EVERY
// PROJECT THAT DECLARES ENVIRONMENTS WAS UNPUBLISHABLE. Not a wrong number: no
// deploy at all.
func TestAProjectWithEnvironmentsCompiles(t *testing.T) {
	fs, dir := envFixture(t)

	for _, env := range []string{"production", "staging", "preview", "ab"} {
		_, _, err := publish.Compile(fs, dir, "main", env)
		require.NoError(t, err, env)
	}
}

// AND EACH DEPLOYMENT GETS ITS OWN NUMBERS. Fifty-one attributes may be written
// per environment; publishing one object under several names would give every
// deployment production's limits.
func TestEachDeploymentResolvesItsOwnValues(t *testing.T) {
	fs, dir := envFixture(t)

	limits := func(env string) (time.Duration, int64) {
		_, p, err := publish.Compile(fs, dir, "main", env)
		require.NoError(t, err)

		r, _, err := p.Resource(nil, "function", "checkout")
		require.NoError(t, err)

		timeout, ok := r.Duration("execution", "timeout")
		require.True(t, ok, env)
		memory, ok := r.Int("execution", "memory")
		require.True(t, ok, env)
		return timeout, memory
	}

	// production is the unsuffixed line.
	timeout, memory := limits("production")
	require.Equal(t, 30*time.Second, timeout)
	require.Equal(t, int64(64_000_000), memory, "64MB is decimal here")

	// preview suffixes both.
	timeout, memory = limits("preview")
	require.Equal(t, 5*time.Second, timeout)
	require.Equal(t, int64(16_000_000), memory)

	// staging suffixes only the memory, so the timeout falls back to the
	// unsuffixed one — which is what makes a partial override the common case
	// rather than an all-or-nothing copy of the document.
	timeout, memory = limits("staging")
	require.Equal(t, 30*time.Second, timeout)
	require.Equal(t, int64(32_000_000), memory)

	// a shadow reads its own suffix, not its parent's.
	timeout, _ = limits("ab")
	require.Equal(t, 45*time.Second, timeout)
}

// A PUSH IS ALREADY AN ANSWER TO "DEPLOY WHERE".
func TestTheBranchDecidesTheDeployments(t *testing.T) {
	fs, dir := envFixture(t)

	got, err := publish.Environments(fs, dir, "main", "")
	require.NoError(t, err)
	require.Equal(t, []string{"preview", "production", "ab"}, got)

	got, err = publish.Environments(fs, dir, "release/1.4", "")
	require.NoError(t, err)
	require.Equal(t, []string{"preview", "staging"}, got)

	// An explicit name wins over the branch.
	got, err = publish.Environments(fs, dir, "main", "staging")
	require.NoError(t, err)
	require.Equal(t, []string{"staging"}, got)
}

func TestWhatEnvironmentsRefuses(t *testing.T) {
	fs, dir := envFixture(t)

	// A MISSPELLED ENVIRONMENT IS NOT A NEW ONE. Taking it would publish under
	// a pointer nothing ever reads again, and the deploy would look like it
	// worked.
	_, err := publish.Environments(fs, dir, "main", "prodution")
	require.ErrorIs(t, err, publish.ErrNoSuchEnvironment)
	require.ErrorContains(t, err, "production")

	// Nothing says where to deploy.
	_, err = publish.Environments(fs, dir, "", "")
	require.ErrorIs(t, err, publish.ErrNoBranch)

	// A branch no deployment claims, on a project whose deployments are all
	// claimed by globs — possible only when nothing declares "*".
	_, err = publish.Environments(fs, dir, "detached", "")
	require.NoError(t, err, "the fixture's preview takes every branch")
}

// A project with no table has one deployment, and the name is the caller's.
func TestNoTableIsOneDeployment(t *testing.T) {
	fs, dir := fixture(t)

	got, err := publish.Environments(fs, dir, "main", "")
	require.NoError(t, err)
	require.Equal(t, []string{publish.DefaultEnvironment}, got)

	got, err = publish.Environments(fs, dir, "main", "whatever")
	require.NoError(t, err)
	require.Equal(t, []string{"whatever"}, got)
}
