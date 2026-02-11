package main

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
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
		call, err := prompts.GetOrRequireACall(ctx, source)
		if err != nil {
			return err
		}

		// Edit, sending empty cli context so that the flags are not set
		call, err = prompts.GetOrRequireACall(&cli.Context{}, source, call)
		if err != nil {
			return err
		}

		printer.Out.SuccessPrintfln("Got call: `%s`", call)
		return nil
	},
}
