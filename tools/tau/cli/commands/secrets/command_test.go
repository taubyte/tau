package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"

	buildClient "github.com/taubyte/tau/tools/tau/clients/build"
	"github.com/taubyte/tau/tools/tau/tcc"
)

// configAt writes a config repo with the given project.yaml body and secret
// declarations, and opens a store over it — the same thing `tau` opens against
// a developer's clone.
func configAt(t *testing.T, project string, secrets map[string]string) *tcc.Store {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.yaml"), []byte(project), 0o644))

	for name, body := range secrets {
		full := filepath.Join(dir, "secret", name+".yaml")
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}

	store, err := tcc.OpenAt(dir)
	require.NoError(t, err)
	return store
}

const oneEnvironment = "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n" +
	"name: shop\n" +
	"environments:\n    production:\n        branches: [main]\n"

const twoEnvironments = oneEnvironment +
	"    staging:\n        branches: [\"release/*\"]\n"

// ── which deployment ─────────────────────────────────────────────────────────

func TestTheOnlyDeploymentNeedsNoFlag(t *testing.T) {
	store := configAt(t, oneEnvironment, nil)

	name, err := environmentOf(contextWith(t, ""), store)
	require.NoError(t, err)
	require.Equal(t, "production", name)
}

// TestTwoDeploymentsAreNotGuessedBetween. Writing production's database
// password into staging is silent, and it is discovered when production stops
// working.
func TestTwoDeploymentsAreNotGuessedBetween(t *testing.T) {
	store := configAt(t, twoEnvironments, nil)

	_, err := environmentOf(contextWith(t, ""), store)
	require.ErrorContains(t, err, "more than one deployment")
	require.ErrorContains(t, err, "production, staging", "it says which")

	name, err := environmentOf(contextWith(t, "staging"), store)
	require.NoError(t, err)
	require.Equal(t, "staging", name)
}

func TestAProjectWithNoDeploymentsSaysSo(t *testing.T) {
	store := configAt(t, "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: shop\n", nil)

	_, err := environmentOf(contextWith(t, ""), store)
	require.ErrorContains(t, err, "declares no deployments")
}

// ── a name is not the key ────────────────────────────────────────────────────

// TestANameBecomesTheIdTheVaultIsKeyedBy. A reference compiles to an id, so
// that is what the cloud holds; a person types the name.
func TestANameResolvesToItsDeclaredId(t *testing.T) {
	s := &scope{byName: map[string]string{"stripe": "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy"}}

	id, err := s.secretID("stripe")
	require.NoError(t, err)
	require.Equal(t, "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy", id)
}

// TestSettingAnUndeclaredSecretIsRefused. It would succeed at the cloud and do
// nothing at all, and the failure would surface as a workload that never got
// its variable.
func TestAnUndeclaredSecretIsRefusedBeforeAnythingIsSent(t *testing.T) {
	s := &scope{byName: map[string]string{}}

	_, err := s.secretID("stripe")
	require.ErrorContains(t, err, "declares no secret called \"stripe\"")
	require.ErrorContains(t, err, "tau new secret", "it says how to declare one")

	_, err = s.secretID("")
	require.ErrorContains(t, err, "which secret?")
}

// TestTheStoreIsReadForDeclarations, both directions, because list turns an id
// back into a name and set turns a name into an id.
func TestDeclarationsAreReadFromTheConfigRepo(t *testing.T) {
	store := configAt(t, oneEnvironment, map[string]string{
		"stripe": "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\nname: stripe\n",
	})

	names, err := store.List("secret")
	require.NoError(t, err)
	require.Equal(t, []string{"stripe"}, names)

	doc, err := store.Doc("secret", "stripe")
	require.NoError(t, err)
	require.Equal(t, "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy", doc["id"])
}

// ── where the value comes from ───────────────────────────────────────────────

func TestAValueComesFromAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(path, []byte("sk_live\n"), 0o600))

	value, err := readValue(valueContext(t, "--from-file", path))
	require.NoError(t, err)
	require.Equal(t, "sk_live\n", string(value), "the file's bytes, trimmed of nothing")
}

func TestAValueComesFromTheFlag(t *testing.T) {
	value, err := readValue(valueContext(t, "--value", "sk_live"))
	require.NoError(t, err)
	require.Equal(t, "sk_live", string(value))
}

// TestTwoAnswersToOneQuestionIsRefused rather than one of them silently
// winning.
func TestValueAndFromFileTogetherAreRefused(t *testing.T) {
	_, err := readValue(valueContext(t, "--value", "a", "--from-file", "b"))
	require.ErrorContains(t, err, "two answers to one question")
}

// ── helpers ──────────────────────────────────────────────────────────────────

// contextWith runs a one-flag app so the context is the real thing urfave
// builds, including IsSet — which readValue depends on.
func contextWith(t *testing.T, environment string) *cli.Context {
	t.Helper()

	args := []string{"app"}
	if environment != "" {
		args = append(args, "--env", environment)
	}
	return runFor(t, []cli.Flag{environmentFlag}, args)
}

func valueContext(t *testing.T, args ...string) *cli.Context {
	t.Helper()
	return runFor(t, []cli.Flag{
		&cli.StringFlag{Name: "value"},
		&cli.StringFlag{Name: "from-file"},
	}, append([]string{"app"}, args...))
}

func runFor(t *testing.T, flags []cli.Flag, args []string) *cli.Context {
	t.Helper()

	var captured *cli.Context
	app := &cli.App{
		Flags:  flags,
		Action: func(ctx *cli.Context) error { captured = ctx; return nil },
	}
	require.NoError(t, app.Run(args))
	return captured
}

// ── what reaches the cloud ───────────────────────────────────────────────────

// fakeClient records what was asked of the cloud.
type fakeClient struct {
	buildClient.Client

	set    [][3]string
	delete [][3]string
	names  []string
	err    error
}

func (f *fakeClient) SetSecret(_ context.Context, project, environment, name string, value []byte) error {
	f.set = append(f.set, [3]string{project + "/" + environment, name, string(value)})
	return f.err
}

func (f *fakeClient) DeleteSecret(_ context.Context, project, environment, name string) error {
	f.delete = append(f.delete, [3]string{project + "/" + environment, name, ""})
	return f.err
}

func (f *fakeClient) Secrets(_ context.Context, _, _ string) ([]string, error) {
	return f.names, f.err
}

func testScope(c buildClient.Client) *scope {
	return &scope{
		client: c, project: "QmProject", environment: "production",
		names:  map[string]string{"QmStripe": "stripe"},
		byName: map[string]string{"stripe": "QmStripe"},
	}
}

// TestTheCloudIsAskedForTheIdAndNotTheName, which is the one thing that has to
// be right for a rename in git not to detach a secret from its value.
func TestSetSendsTheIdAndNeverEchoesTheValue(t *testing.T) {
	c := &fakeClient{}
	var out strings.Builder

	require.NoError(t, testScope(c).set(t.Context(), "stripe", []byte("sk_live_dontleakme"), &out))
	require.Equal(t, [][3]string{{"QmProject/production", "QmStripe", "sk_live_dontleakme"}}, c.set)

	require.Contains(t, out.String(), "stripe is set in production")
	require.NotContains(t, out.String(), "sk_live", "a terminal is scrolled back through and screen-shared")
}

func TestRemoveSendsTheId(t *testing.T) {
	c := &fakeClient{}
	var out strings.Builder

	require.NoError(t, testScope(c).remove(t.Context(), "stripe", &out))
	require.Equal(t, [][3]string{{"QmProject/production", "QmStripe", ""}}, c.delete)
	require.Contains(t, out.String(), "stripe is unset in production")
}

// TestAListShowsNames, turning the ids the cloud holds back into what a person
// wrote.
func TestListShowsNamesAndFlagsWhatIsNoLongerDeclared(t *testing.T) {
	c := &fakeClient{names: []string{"QmStripe", "QmGone"}}
	var out strings.Builder

	require.NoError(t, testScope(c).list(t.Context(), &out))
	require.Contains(t, out.String(), "stripe")

	// A value set under an id this config no longer declares is holding a
	// value, and somebody has to see it in order to remove it.
	require.Contains(t, out.String(), "QmGone (not declared in this config)")
}

func TestAnEmptyListSaysSoRatherThanPrintingNothing(t *testing.T) {
	var out strings.Builder
	require.NoError(t, testScope(&fakeClient{}).list(t.Context(), &out))
	require.Contains(t, out.String(), "nothing is set in production")
}

// TestNothingIsSentForAnUndeclaredName. The refusal happens before the call,
// so a typo does not leave a value in the cloud under a name nothing reads.
func TestAnUndeclaredNameSendsNothing(t *testing.T) {
	c := &fakeClient{}
	var out strings.Builder

	require.Error(t, testScope(c).set(t.Context(), "typo", []byte("v"), &out))
	require.Error(t, testScope(c).remove(t.Context(), "typo", &out))
	require.Empty(t, c.set)
	require.Empty(t, c.delete)
	require.Empty(t, out.String())
}

func TestACloudFailureIsReported(t *testing.T) {
	c := &fakeClient{err: errors.New("the cloud said no")}
	var out strings.Builder

	require.ErrorContains(t, testScope(c).set(t.Context(), "stripe", []byte("v"), &out), "the cloud said no")
	require.ErrorContains(t, testScope(c).remove(t.Context(), "stripe", &out), "the cloud said no")
	require.ErrorContains(t, testScope(c).list(t.Context(), &out), "the cloud said no")
	require.Empty(t, out.String(), "nothing claimed success")
}

// TestTheCommandIsThreeVerbsAndNoGet. The absence is the security property:
// there is no route that returns a value, so there must be no command that
// asks for one.
func TestThereIsNoGet(t *testing.T) {
	verbs := map[string]bool{}
	for _, sub := range Command.Subcommands {
		verbs[sub.Name] = true
	}
	require.Equal(t, map[string]bool{"set": true, "list": true, "rm": true}, verbs)
}

// TestAPipedValueIsRead, so `openssl rand -hex 32 | tau secrets set X` works
// and nothing has to be typed where a shell will remember it.
func TestAValueComesFromStdinWhenItIsAPipe(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	go func() {
		w.WriteString("sk_live")
		w.Close()
	}()

	restore := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = restore })

	value, err := readValue(valueContext(t))
	require.NoError(t, err)
	require.Equal(t, "sk_live", string(value))
}

// TestATerminalIsNotReadFrom. Blocking on a terminal with no prompt reads as a
// hang, so it is refused with the two ways to pass a value instead.
func TestATerminalIsRefusedRatherThanBlockedOn(t *testing.T) {
	terminal, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		t.Skip("no controlling terminal here")
	}
	t.Cleanup(func() { terminal.Close() })

	restore := os.Stdin
	os.Stdin = terminal
	t.Cleanup(func() { os.Stdin = restore })

	_, err = readValue(valueContext(t))
	require.ErrorContains(t, err, "no value")
}

// TestIsTerminalTellsThemApart, which is what the two tests above turn on.
func TestIsTerminalTellsAPipeFromATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { r.Close(); w.Close() })
	require.False(t, isTerminal(r), "a pipe is not a terminal")

	null, err := os.Open(os.DevNull)
	require.NoError(t, err)
	t.Cleanup(func() { null.Close() })
	require.True(t, isTerminal(null), "/dev/null is a character device, like a tty")

	// A closed file cannot be asked, and that is not a terminal either.
	closed, err := os.Open(os.DevNull)
	require.NoError(t, err)
	require.NoError(t, closed.Close())
	require.False(t, isTerminal(closed))
}
