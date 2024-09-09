package flags

import (
	"github.com/taubyte/tau/pkg/cli/common"
	"github.com/urfave/cli/v2"
)

var Language = &cli.StringFlag{
	Name:    "language",
	Aliases: []string{"lang"},
	Usage:   "Template language; " + UsageOneOfOption(common.GetLanguages()),
}
