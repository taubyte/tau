package flags

import "github.com/urfave/cli/v2"

var Call = &cli.StringFlag{
	Name:    "call",
	Aliases: []string{"ca"},
	Usage:   "Exported function to call",
}
