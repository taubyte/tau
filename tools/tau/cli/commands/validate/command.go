package validate

import (
	"github.com/urfave/cli/v2"
)

var branchFlag = &cli.StringFlag{
	Name:    "branch",
	Aliases: []string{"b"},
	Usage:   "Branch to validate against; if unset, uses current branch",
}

var Command = &cli.Command{
	Name:  "validate",
	Usage: "Validate project configuration",
	Subcommands: []*cli.Command{
		{
			Name:   "config",
			Usage:  "Validate the selected project's config",
			Flags:  []cli.Flag{branchFlag},
			Action: runValidateConfig,
		},
	},
}
