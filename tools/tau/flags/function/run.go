package functionFlags

import (
	"github.com/urfave/cli/v2"
)

var (
	RunWasm = &cli.StringFlag{
		Name:     "wasm",
		Usage:    "Path to compiled WASM file",
		Category: CategoryRun,
	}

	RunBody = &cli.StringFlag{
		Name:     "body",
		Usage:    "Request body (literal string or @filepath)",
		Category: CategoryRun,
	}

	RunHeader = &cli.StringSliceFlag{
		Name:     "header",
		Aliases:  []string{"H"},
		Usage:    "Request header as Key: Value (repeatable)",
		Category: CategoryRun,
	}

	RunMethod = &cli.StringFlag{
		Name:     "method",
		Aliases:  []string{"m"},
		Usage:    "Override HTTP method from function spec",
		Category: CategoryRun,
	}

	RunPath = &cli.StringFlag{
		Name:     "path",
		Usage:    "Override URL path from function spec",
		Category: CategoryRun,
	}

	RunDomain = &cli.StringFlag{
		Name:     "domain",
		Usage:    "Override Host header from function spec",
		Category: CategoryRun,
	}

	RunTimeout = &cli.DurationFlag{
		Name:     "timeout",
		Usage:    "Override function timeout",
		Category: CategoryRun,
	}

	RunForceBuild = &cli.BoolFlag{
		Name:     "force-build",
		Usage:    "Rebuild the function before running (no prompt)",
		Category: CategoryRun,
	}
)

func RunFlags() []cli.Flag {
	return []cli.Flag{
		RunWasm,
		RunBody,
		RunHeader,
		RunMethod,
		RunPath,
		RunDomain,
		RunTimeout,
		RunForceBuild,
	}
}
