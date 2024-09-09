package flags

import "github.com/urfave/cli/v2"

var Path = &cli.StringFlag{
	Name:    "path",
	Aliases: []string{"p"},
}
