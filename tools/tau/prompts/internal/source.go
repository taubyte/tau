package main

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var SourceCommand = &cli.Command{
	Name: "source",
	Flags: []cli.Flag{
		flags.Source,
	},
	Action: func(ctx *cli.Context) error {

		// New
		source, err := prompts.GetOrSelectSource(ctx)
		if err != nil {
			return err
		}

		// Edit, sending empty cli context so that the flags are not set
		source, err = prompts.GetOrSelectSource(&cli.Context{}, source.String())
		if err != nil {
			return err
		}

		pterm.Success.Printfln("Got source: `%s`", source)
		return nil
	},
}
