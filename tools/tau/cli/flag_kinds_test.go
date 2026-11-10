package cli

import (
	"testing"

	argsLib "github.com/taubyte/tau/tools/tau/cli/args"
	"github.com/urfave/cli/v2"
)

// The commit message is a value however it reads, and flags whose kind differs
// between commands (push --code, import --code VALUE) parse as they always did.
func TestRealCommandsParseFlagValues(t *testing.T) {
	app, err := New()
	if err != nil {
		t.Fatal(err)
	}
	parse := func(args ...string) []string {
		return argsLib.ParseArguments(app.Flags, app.Commands, append([]string{"tau"}, args...)...)
	}
	for _, c := range []struct{ in, want []string }{
		{[]string{"push", "project", "-m", "cancel"}, []string{"tau", "push", "project", "-m", "cancel"}},
		{[]string{"push", "project", "--code-only", "-m", "new"}, []string{"tau", "push", "project", "--code-only", "-m", "new"}},
		{[]string{"new", "function", "--name", "query"}, []string{"tau", "new", "function", "--name", "query"}},
		{[]string{"query", "logs", "--jid", "j", "-f"}, []string{"tau", "query", "logs", "--jid", "j", "-f"}},
		{[]string{"logs", "j1", "-f"}, []string{"tau", "logs", "-f", "j1"}},
		{[]string{"logs", "--last", "website", "site1"}, []string{"tau", "logs", "--last", "website", "site1"}},
	} {
		got := parse(c.in...)
		if len(got) != len(c.want) {
			t.Errorf("%v: got %v", c.in, got)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%v: got %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
	_ = cli.Command{}
}
