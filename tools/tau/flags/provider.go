package flags

import "github.com/urfave/cli/v2"

var Provider = &cli.StringFlag{
	Name:        "provider",
	DefaultText: "github",
	Usage:       "Git provider",
}
