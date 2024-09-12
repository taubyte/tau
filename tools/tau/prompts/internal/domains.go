package main

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var DomainsCommand = &cli.Command{
	Name: "domains",
	Flags: []cli.Flag{
		flags.Domains,
	},
	Action: func(ctx *cli.Context) error {

		// New
		domains, err := prompts.GetOrSelectDomainsWithFQDN(ctx)
		if err != nil {
			return err
		}

		// Edit, sending empty cli context so that the flags are not set
		domains, err = prompts.GetOrSelectDomainsWithFQDN(&cli.Context{}, domains...)
		if err != nil {
			return err
		}

		pterm.Success.Printfln("Got domains: `%v`", domains)
		return nil
	},
}
