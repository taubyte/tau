package flags

import (
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/urfave/cli/v2"
)

var Branch = &cli.StringFlag{
	Name:    "branch",
	Aliases: []string{"b"},
	Value:   common.DefaultNewProjectBranch,
}
