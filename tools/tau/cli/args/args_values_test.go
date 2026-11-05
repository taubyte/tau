package args

import (
	"slices"
	"testing"

	"github.com/urfave/cli/v2"
)

// A flag's value that happens to be a command's name is still a value.
func TestFlagValueIsNotACommand(t *testing.T) {
	message := &cli.StringFlag{Name: "message", Aliases: []string{"m"}}
	name := &cli.StringFlag{Name: "name"}
	only := &cli.BoolFlag{Name: "code-only"}
	commands := []*cli.Command{
		{Name: "cancel"},
		{Name: "new", Subcommands: []*cli.Command{{Name: "function", Flags: []cli.Flag{name}}}},
		{Name: "push", Subcommands: []*cli.Command{{Name: "project", Flags: []cli.Flag{message, only}}}},
		{Name: "query", Subcommands: []*cli.Command{{Name: "logs"}}},
	}
	global := []cli.Flag{&cli.BoolFlag{Name: "yes"}, &cli.StringFlag{Name: "color"}}

	for _, c := range []struct{ in, want []string }{
		{
			[]string{"tau", "push", "project", "--code-only", "-m", "cancel"},
			[]string{"tau", "push", "project", "--code-only", "-m", "cancel"},
		},
		{
			[]string{"tau", "push", "project", "-m", "new"},
			[]string{"tau", "push", "project", "-m", "new"},
		},
		{
			[]string{"tau", "push", "project", "--message", "query"},
			[]string{"tau", "push", "project", "--message", "query"},
		},
		{
			[]string{"tau", "new", "function", "--name", "push"},
			[]string{"tau", "new", "function", "--name", "push"},
		},
		{ // commands after flags still move to the front
			[]string{"tau", "--yes", "push", "project", "-m", "x"},
			[]string{"tau", "--yes", "push", "project", "-m", "x"},
		},
		{ // a bool flag takes no value: the word after it is the command
			[]string{"tau", "--yes", "query", "logs"},
			[]string{"tau", "--yes", "query", "logs"},
		},
	} {
		got := ParseArguments(global, commands, slices.Clone(c.in)...)
		if !slices.Equal(got, c.want) {
			t.Errorf("%v\n got %v\nwant %v", c.in, got, c.want)
		}
	}
}
