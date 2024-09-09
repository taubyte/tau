package flags

import "github.com/urfave/cli/v2"

var EmbedToken = &BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:    "embed-token",
		Aliases: []string{"e"},
		Usage:   "Embed git token into remote url",
	},
}
