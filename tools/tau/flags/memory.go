package flags

import (
	"github.com/taubyte/tau/pkg/cli/common"
	"github.com/urfave/cli/v2"
)

var (
	Memory = &cli.StringFlag{
		Name:    "memory",
		Aliases: []string{"me"},
		Usage:   "Max memory either in form 10GB or 10",
	}

	MemoryUnit = &cli.StringFlag{
		Name:    "memory-unit",
		Aliases: []string{"mu"},
		Usage:   "Unit if not provided with memory; " + UsageOneOfOption(common.SizeUnitTypes),
	}
)
