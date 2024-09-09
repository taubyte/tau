package loginFlags

import "github.com/urfave/cli/v2"

var Token = &cli.StringFlag{
	Name:    "token",
	Aliases: []string{"t"},
	Usage:   "Token from the git provider (e.g. GitHub) to log in with.",
}
