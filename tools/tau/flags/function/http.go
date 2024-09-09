package functionFlags

import (
	"github.com/taubyte/tau/pkg/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

var (
	Method = &cli.StringFlag{
		Name:     "method",
		Aliases:  []string{"m"},
		Category: CategoryHttp,
		Usage:    flags.UsageOneOfOption(common.HTTPMethodTypes),
	}

	Domains = &cli.StringSliceFlag{
		Name:     flags.Domains.Name,
		Aliases:  flags.Domains.Aliases,
		Category: CategoryHttp,
	}

	Paths = &cli.StringSliceFlag{
		Name:     flags.Paths.Name,
		Aliases:  flags.Paths.Aliases,
		Category: CategoryHttp,
	}

	Generate = &cli.BoolFlag{
		Name:     "generate-domain",
		Aliases:  []string{"g"},
		Usage:    "Generates a new domain if no domains found",
		Category: CategoryHttp,
	}
)

func Http() []cli.Flag {
	return []cli.Flag{
		Method,
		Domains,
		Paths,
		Generate,
	}
}
