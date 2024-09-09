package main

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var ServiceCommand = &cli.Command{
	Name: "service",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name: "service",
		},
	},
	Action: func(ctx *cli.Context) error {
		var (
			flag   = "service"
			prompt = "Select a Service:"
		)

		// New
		service, err := prompts.SelectAServiceWithProtocol(ctx, flag, prompt)
		if err != nil {
			return err
		}

		// Edit, sending empty cli context so that the flags are not set
		service, err = prompts.SelectAServiceWithProtocol(&cli.Context{}, flag, prompt, service)
		if err != nil {
			return err
		}

		pterm.Success.Printfln("Got services: `%v`", service)
		return nil
	},
}
