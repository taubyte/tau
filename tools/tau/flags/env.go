package flags

import "github.com/urfave/cli/v2"

var Env = &cli.BoolFlag{
	Name:    "env",
	EnvVars: []string{"TAU_ENV"},
}
