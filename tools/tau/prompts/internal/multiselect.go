package main

import (
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var MultiSelectCommand = &cli.Command{
	Name: "multiselect",
	Flags: []cli.Flag{
		&cli.StringSliceFlag{
			Name: "fruits",
		},
	},
	Action: func(ctx *cli.Context) error {

		cnf := &prompts.MultiSelectConfig{
			Field:    "fruits",
			Prompt:   "Fruits:",
			Options:  []string{"apple", "banana", "orange"},
			Required: true,
		}

		// New
		cnf.Previous = prompts.MultiSelect(ctx, *cnf)

		// Edit
		cnf.Required = false
		fruits := prompts.MultiSelect(&cli.Context{}, *cnf)

		printer.Out.SuccessPrintfln("Got fruits: `%v`", fruits)
		return nil
	},
}
