package flags

import (
	"github.com/urfave/cli/v2"
)

var Local = &BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:    "local",
		Aliases: []string{"l"},
	},
}
