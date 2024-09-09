package flags

import "github.com/urfave/cli/v2"

var Tags = &cli.StringSliceFlag{
	Name:    "tags",
	Aliases: []string{"t"},
}
