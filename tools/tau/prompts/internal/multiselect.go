package main

import (
	"github.com/pterm/pterm"
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

		pterm.Success.Printfln("Got fruits: `%v`", fruits)
		return nil
	},
}
