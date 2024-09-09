package main

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var CallCommand = &cli.Command{
	Name: "call",
	Flags: []cli.Flag{
		flags.Source,
		flags.Call,
	},
	Action: func(ctx *cli.Context) error {

		source, err := prompts.GetOrSelectSource(ctx)
		if err != nil {
			return err
		}

		// New
		call := prompts.GetOrRequireACall(ctx, source)

		// Edit, sending empty cli context so that the flags are not set
		call = prompts.GetOrRequireACall(&cli.Context{}, source, call)

		pterm.Success.Printfln("Got call: `%s`", call)
		return nil
	},
}
