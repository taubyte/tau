package flags

import (
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

var Universe = cli.StringFlag{
	Name:        "universe",
	Aliases:     []string{"u", "to"},
	DefaultText: common.DefaultUniverseName,
	Value:       common.DefaultUniverseName,
}
