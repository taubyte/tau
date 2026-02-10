package cloud

import "github.com/urfave/cli/v2"

var Default = &cli.BoolFlag{
	Name:  "default",
	Usage: "Set cloud to the default sandbox.",
}
