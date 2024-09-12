package build

import (
	"github.com/taubyte/tau/tools/tau/cli/common/options"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/i18n"
	"github.com/urfave/cli/v2"
)

func attachName0(commands []*cli.Command) []*cli.Command {
	for _, cmd := range commands {
		cmd.Flags = append(cmd.Flags, flags.Name)
		cmd.ArgsUsage = i18n.ArgsUsageName
		cmd.Before = options.SetNameAsArgs0
	}

	return commands
}

var Command = &cli.Command{
	Name: "build",
	Subcommands: attachName0([]*cli.Command{
		{
			Name:   "config",
			Action: buildConfig,
		},
		{
			Name:   "code",
			Action: buildCode,
		},
		{
			Name:   "function",
			Action: buildFunction,
		},
		{
			Name:   "website",
			Action: buildWebsite,
		},
		{
			Name:   "library",
			Action: buildLibrary,
		},
	}),
}
