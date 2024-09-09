package flags

import "github.com/urfave/cli/v2"

var Name = &cli.StringFlag{
	Name:    "name",
	Aliases: []string{"n"},
}
