package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

// every visible command, at every depth, says what it does
func TestEveryCommandHasUsage(t *testing.T) {
	app := defineCLI(&common.Context{Ctx: t.Context()})
	var walk func(path string, cmds []*cli.Command)
	walk = func(path string, cmds []*cli.Command) {
		for _, c := range cmds {
			if c.Hidden {
				continue
			}
			if strings.TrimSpace(c.Usage) == "" {
				t.Errorf("%s %s: empty Usage", path, c.Name)
			}
			walk(path+" "+c.Name, c.Subcommands)
		}
	}
	walk("dream", app.Commands)
	if app.Usage == "" || app.Name != "dream" {
		t.Errorf("app name/usage: %q %q", app.Name, app.Usage)
	}
}

func runApp(t *testing.T, args ...string) (string, int, error) {
	app := defineCLI(&common.Context{Ctx: t.Context()})
	var out bytes.Buffer
	app.Writer, app.ErrWriter = &out, &out
	code := -1
	old := cli.OsExiter
	cli.OsExiter = func(c int) { code = c }
	defer func() { cli.OsExiter = old }()
	err := app.Run(append([]string{"dream"}, args...))
	return out.String(), code, err
}

func TestUnknownCommandSuggests(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"sttaus"}, "Did you mean `status`?"},
		{[]string{"injct"}, "Did you mean `inject`?"},
		{[]string{"status", "univrse"}, "Did you mean `universe`?"},
	} {
		out, code, _ := runApp(t, tc.args...)
		if !strings.Contains(out, tc.want) || code != 3 {
			t.Errorf("%v: code %d, output %q", tc.args, code, out)
		}
	}
}

func TestFlagsAfterName(t *testing.T) {
	// fails while reading the name, before any call to Dream
	_, _, err := runApp(t, "kill", "universe", "blackhole", "--name", "x")
	if !errors.Is(err, common.ErrFlagsAfterArgs) {
		t.Fatalf("got %v", err)
	}
}
