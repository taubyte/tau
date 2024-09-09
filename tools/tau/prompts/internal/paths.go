package main

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var PathsCommand = &cli.Command{
	Name: "paths",
	Flags: []cli.Flag{
		flags.Paths,
	},
	Action: func(ctx *cli.Context) error {

		// New
		paths := prompts.RequiredPaths(ctx)

		// Edit, sending empty cli context so that the flags are not set
		paths = prompts.RequiredPaths(&cli.Context{}, paths...)

		pterm.Success.Printfln("Got paths: `%s`", paths)
		return nil
	},
}
