package flags

import "github.com/urfave/cli/v2"

var Debug = &cli.BoolFlag{
	Name:    "debug",
	Usage:   "Show the original error and debug output",
	EnvVars: []string{"TAU_DEBUG"},
}
