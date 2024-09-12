package network

import "github.com/urfave/cli/v2"

var Deprecated = &cli.BoolFlag{
	Name:  "deprecated",
	Usage: "Set network to the deprecated sandbox.",
}
