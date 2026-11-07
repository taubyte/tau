package flags

import "github.com/urfave/cli/v2"

var Names = cli.StringFlag{
	Name:    "names",
	Aliases: []string{"n"},
	Usage:   "Comma-separated names; same as giving them as the first argument",
}
