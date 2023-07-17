package common

import "github.com/urfave/cli/v2"

var BasicFlags = []cli.Flag{
	&cli.BoolFlag{
		Name: "dev",
	},
}
