// cli_test.go is an EXTERNAL test package on purpose: it reads the real `tau`
// command tree, which imports this package, so an internal test doing the same
// would be an import cycle.
package secrets_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/tools/tau/cli"
	argsLib "github.com/taubyte/tau/tools/tau/cli/args"
)

// TestTheCommandParsesTheWayAPersonTypesIt.
//
// tau-cli does not hand its arguments to urfave as they were typed: it moves
// options ahead of positionals first (tools/tau/cli/args), so `... set NAME
// --value X` becomes `... set --value X NAME`. That reordering is what makes
// the name a positional rather than the value of a flag, and it is the one
// thing about this command that is decided outside it — so it is asserted here
// rather than assumed.
func TestSecretsArgumentsSurviveTheCLIsOwnReordering(t *testing.T) {
	app, err := cli.New()
	require.NoError(t, err)

	for _, c := range []struct {
		typed []string
		want  []string
	}{
		{
			[]string{"tau", "secrets", "set", "stripe", "--value", "x"},
			[]string{"tau", "secrets", "set", "--value", "x", "stripe"},
		},
		{
			[]string{"tau", "secrets", "set", "stripe", "--from-file", "-", "--env", "production"},
			[]string{"tau", "secrets", "set", "--from-file", "-", "--env", "production", "stripe"},
		},
		{
			[]string{"tau", "secrets", "rm", "stripe", "--env", "staging"},
			[]string{"tau", "secrets", "rm", "--env", "staging", "stripe"},
		},
		{
			[]string{"tau", "secrets", "list", "--env", "production"},
			[]string{"tau", "secrets", "list", "--env", "production"},
		},
	} {
		require.Equal(t, c.want, argsLib.ParseArguments(app.Flags, app.Commands, c.typed...),
			"typed: %v", c.typed)
	}
}

// TestTheCommandIsMounted, because a command nobody can reach is a feature
// nobody has.
func TestSecretsIsMountedOnTheApp(t *testing.T) {
	app, err := cli.New()
	require.NoError(t, err)

	for _, c := range app.Commands {
		if c.Name == "secrets" {
			return
		}
	}
	t.Fatal("`tau secrets` is not mounted")
}
