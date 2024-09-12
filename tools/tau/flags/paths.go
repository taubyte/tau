package flags

import "github.com/urfave/cli/v2"

var Paths = &cli.StringSliceFlag{
	Name:    "paths",
	Aliases: []string{"p"},
	Usage:   "HTTP paths to use for the endpoint",
}
