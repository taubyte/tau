package cloud

import "github.com/urfave/cli/v2"

var Deprecated = &cli.BoolFlag{
	Name:  "deprecated",
	Usage: "Set cloud to the deprecated sandbox.",
}
