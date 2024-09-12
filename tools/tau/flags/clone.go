package flags

import "github.com/urfave/cli/v2"

var Clone = &BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:  "clone",
		Usage: "Clone an attached repository (Unused for generated repositories)",
	},
}
