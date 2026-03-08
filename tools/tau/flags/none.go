package flags

import "github.com/urfave/cli/v2"

var None = &cli.BoolFlag{
	Name:  "none",
	Usage: "Set selection to none (equivalent to choosing (none) or running clear)",
}
