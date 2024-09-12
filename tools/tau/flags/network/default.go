package network

import "github.com/urfave/cli/v2"

var Default = &cli.BoolFlag{
	Name:  "default",
	Usage: "Set network to the default sandbox.",
}
