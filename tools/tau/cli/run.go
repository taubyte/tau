package cli

import (
	"os"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/pkg/cli/i18n"
	argsLib "github.com/taubyte/tau/tools/tau/cli/args"
	"golang.org/x/term"
)

func Run(args ...string) error {
	setColor(term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd())), os.Getenv("NO_COLOR"))
	app, err := New()
	if err != nil {
		return i18n.AppCreateFailed(err)
	}

	if len(args) == 1 {
		return app.Run(args)
	}

	args = argsLib.ParseArguments(app.Flags, app.Commands, args...)

	return app.Run(args)
}

// setColor keeps ANSI colors for a terminal only, and never with NO_COLOR set.
func setColor(tty bool, noColor string) {
	if !tty || noColor != "" {
		pterm.DisableColor()
	}
}
