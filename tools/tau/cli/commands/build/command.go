package build

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

var outputFlag = &cli.StringFlag{
	Name:    "output",
	Aliases: []string{"o"},
	Usage:   "Output file path; if unset, writes to a temp file and prints the path",
}

var Command = &cli.Command{
	Name:  "build",
	Usage: "Build a resource from local clone (function, website, or library)",
	Subcommands: []*cli.Command{
		{
			Name:   "function",
			Usage:  "Build the selected function (WASM)",
			Flags:  []cli.Flag{outputFlag, flags.Name},
			Action: runBuildFunction,
		},
		{
			Name:   "website",
			Usage:  "Build the selected website (zip)",
			Flags:  []cli.Flag{outputFlag, flags.Name},
			Action: runBuildWebsite,
		},
		{
			Name:   "library",
			Usage:  "Build the selected library (WASM)",
			Flags:  []cli.Flag{outputFlag, flags.Name},
			Action: runBuildLibrary,
		},
	},
}
