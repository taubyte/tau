package flags

import (
	"github.com/taubyte/tau/dream"
	"github.com/urfave/cli/v2"
)

var Universe = cli.StringFlag{
	Name:        "universe",
	Aliases:     []string{"u", "to"},
	DefaultText: dream.DefaultUniverseName,
	Value:       dream.DefaultUniverseName,
}
