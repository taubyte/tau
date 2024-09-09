package flags

import "github.com/urfave/cli/v2"

var Branch = &cli.StringFlag{
	Name:    "branch",
	Aliases: []string{"b"},
}
