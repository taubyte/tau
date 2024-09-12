package main

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var LanguageCommand = &cli.Command{
	Name: "language",
	Flags: []cli.Flag{
		flags.Language,
	},
	Action: func(ctx *cli.Context) error {

		// New
		language, err := prompts.GetOrSelectLanguage(ctx)
		if err != nil {
			return err
		}

		// Edit, sending empty cli context so that the flags are not set
		language, err = prompts.GetOrSelectLanguage(&cli.Context{}, language)
		if err != nil {
			return err
		}

		pterm.Success.Printfln("Got language: `%s`", language)
		return nil
	},
}
