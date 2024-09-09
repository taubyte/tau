package flags

import "github.com/urfave/cli/v2"

var EntryPoint = &cli.StringFlag{
	Name:    "entry-point",
	Aliases: []string{"ep"},
}
