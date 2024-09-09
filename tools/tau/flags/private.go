package flags

import "github.com/urfave/cli/v2"

var Private = &BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:  "private",
		Usage: "Visibility of the generated repository",
	},
}
