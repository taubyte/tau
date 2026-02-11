package flags

import "github.com/urfave/cli/v2"

var Defaults = &cli.BoolFlag{
	Name:  "defaults",
	Usage: "Use defaults when possible for any command; fail with a clear error when a value is required (for scripts/AI)",
}
