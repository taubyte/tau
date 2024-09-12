package flags

import "github.com/urfave/cli/v2"

var Match = &cli.StringFlag{
	Name:    "match",
	Aliases: []string{"m"},
	Usage:   "[^regex] if configured or /path/to/match",
}

var MatchRegex = &BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:    "regex",
		Aliases: []string{"r"},
		Usage:   "Match using regex",
	},
}
