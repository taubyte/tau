package main

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var testBoolFlag = &flags.BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name: "bool",
	},
}

var BoolCommand = &cli.Command{
	Name: "bool",
	Flags: []cli.Flag{
		testBoolFlag,
	},
	Action: func(ctx *cli.Context) error {

		// New
		value := prompts.GetOrAskForBool(ctx, testBoolFlag.Name, "Provide a boolean:")

		// Edit, sending empty cli context so that the flags are not set
		value = prompts.GetOrAskForBool(&cli.Context{}, testBoolFlag.Name, "Provide a boolean:", value)

		pterm.Success.Printfln("Got bool: `%v`", value)
		return nil
	},
}
