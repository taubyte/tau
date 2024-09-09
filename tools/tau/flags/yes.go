package flags

import "github.com/urfave/cli/v2"

var Yes = &cli.BoolFlag{
	Name:    "yes",
	Aliases: []string{"y"},
	Usage:   "Skip the confirmation prompt",
}
