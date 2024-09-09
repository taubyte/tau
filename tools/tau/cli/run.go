package cli

import (
	"github.com/taubyte/tau/pkg/cli/i18n"
	argsLib "github.com/taubyte/tau/tools/tau/cli/args"
)

func Run(args ...string) error {
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
