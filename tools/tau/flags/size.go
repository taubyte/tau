package flags

import (
	"github.com/taubyte/tau/pkg/cli/common"
	"github.com/urfave/cli/v2"
)

var (
	Size = &cli.StringFlag{
		Name:    "size",
		Aliases: []string{"s"},
		Usage:   "Max size either in form 10GB or 10",
	}

	SizeUnit = &cli.StringFlag{
		Name:    "size-unit",
		Aliases: []string{"su"},
		Usage:   "Unit if not provided with size; " + UsageOneOfOption(common.SizeUnitTypes),
	}
)
