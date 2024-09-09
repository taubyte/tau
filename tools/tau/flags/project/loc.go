package projectFlags

import "github.com/urfave/cli/v2"

var Loc = &cli.StringFlag{
	Name:        "location",
	Aliases:     []string{"loc"},
	Usage:       "where the project is cloned",
	DefaultText: "cwd",
}
