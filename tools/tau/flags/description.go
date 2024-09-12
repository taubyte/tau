package flags

import "github.com/urfave/cli/v2"

var Description = &cli.StringFlag{
	Name:    "description",
	Aliases: []string{"d"},
}
