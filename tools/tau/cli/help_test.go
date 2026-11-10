package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

// Every command and subcommand says what it does in `tau --help`.
func TestEveryCommandHasUsage(t *testing.T) {
	app, err := New()
	assert.NilError(t, err)

	var walk func(path string, cmds []*cli.Command)
	walk = func(path string, cmds []*cli.Command) {
		for _, c := range cmds {
			if c.Hidden {
				continue
			}
			name := strings.TrimSpace(path + " " + c.Name)
			if c.Usage == "" {
				t.Errorf("`tau %s` has no Usage", name)
			}
			walk(name, c.Subcommands)
		}
	}
	walk("", app.Commands)
}

func TestVersionFlagMatchesVersionCommand(t *testing.T) {
	var out bytes.Buffer
	pterm.SetDefaultOutput(&out)
	pterm.DisableColor()
	t.Cleanup(func() { pterm.SetDefaultOutput(nil); pterm.EnableColor() })

	for _, args := range [][]string{{"tau", "version"}, {"tau", "--version"}, {"tau", "-v"}} {
		out.Reset()
		assert.NilError(t, Run(args...))
		assert.Assert(t, strings.Contains(out.String(), common.VersionLine()), "%v printed %q", args, out.String())
	}
}

func TestUnknownCommandSuggests(t *testing.T) {
	err := Run("tau", "nwe")
	assert.ErrorContains(t, err, "Did you mean `tau new`?")

	err = Run("tau", "new", "fucntion")
	assert.ErrorContains(t, err, "Did you mean `tau new function`?")

	err = Run("tau", "zzzzzz")
	assert.ErrorContains(t, err, `"zzzzzz" is not a`)
	assert.Assert(t, !strings.Contains(err.Error(), "Did you mean"))
}

func TestSuggest(t *testing.T) {
	cmds := []*cli.Command{{Name: "push"}, {Name: "pull"}, {Name: "secret", Hidden: true}}
	assert.Equal(t, suggest("psh", cmds), "push")
	assert.Equal(t, suggest("PUL", cmds), "pull")
	assert.Equal(t, suggest("secrt", cmds), "")
	assert.Equal(t, distance("kitten", "sitting"), 3)
}
