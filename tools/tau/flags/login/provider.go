package loginFlags

import "github.com/urfave/cli/v2"

var Provider = &cli.StringFlag{
	Name:        "provider",
	Aliases:     []string{"p"},
	DefaultText: "github",
	Usage:       "Provider to log in with. Currently, only GitHub is supported.",
}
