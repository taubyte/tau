package flags

import "github.com/urfave/cli/v2"

var UseCodeTemplate = &BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name: "use-template",
	},
}
