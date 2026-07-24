package generic

// Flags of the local run: what to run and what request to run it against.

import (
	"github.com/urfave/cli/v2"
)

var (
	RunWasm = &cli.StringFlag{
		Name:  "wasm",
		Usage: "Path to compiled WASM file",
	}

	RunBody = &cli.StringFlag{
		Name:  "body",
		Usage: "Request body (literal string or @filepath)",
	}

	RunHeader = &cli.StringSliceFlag{
		Name:    "header",
		Aliases: []string{"H"},
		Usage:   "Request header as Key: Value (repeatable)",
	}

	RunMethod = &cli.StringFlag{
		Name:    "method",
		Aliases: []string{"m"},
		Usage:   "Override HTTP method from function spec",
	}

	RunPath = &cli.StringFlag{
		Name:  "path",
		Usage: "Override URL path from function spec",
	}

	RunDomain = &cli.StringFlag{
		Name:  "domain",
		Usage: "Override Host header from function spec",
	}

	RunTimeout = &cli.DurationFlag{
		Name:  "timeout",
		Usage: "Override function timeout",
	}

	RunForceBuild = &cli.BoolFlag{
		Name:  "force-build",
		Usage: "Rebuild the function before running (no prompt)",
	}
)

func runFlags() []cli.Flag {
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
