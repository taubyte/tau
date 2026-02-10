package main

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
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

		printer.Out.SuccessPrintfln("Got paths: `%s`", paths)
		return nil
	},
}
