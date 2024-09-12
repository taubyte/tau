package main

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/pkg/schema/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var MemoryCommand = &cli.Command{
	Name: "memory",
	Flags: []cli.Flag{
		flags.Memory,
		flags.MemoryUnit,
	},
	Action: func(ctx *cli.Context) error {

		// New
		size, err := prompts.GetOrRequireMemoryAndType(ctx, true)
		if err != nil {
			return err
		}

		// Edit, sending empty cli context so that the flags are not set
		size, err = prompts.GetOrRequireMemoryAndType(&cli.Context{}, false, size)
		if err != nil {
			return err
		}

		pterm.Success.Printfln("Got memory size: `%s`", common.UnitsToString(size))
		return nil
	},
}
