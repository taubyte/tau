package main

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/flags"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var SelectRepositoryCommand = &cli.Command{
	Name: "select_repository",
	Flags: flags.Combine(
		flags.RepositoryId,
		flags.RepositoryName,
	),
	Action: func(ctx *cli.Context) (err error) {
		// New
		selected, err := prompts.SelectARepository(ctx, &repositoryLib.Info{
			Type: repositoryLib.WebsiteRepositoryType,
		})
		if err != nil {
			return err
		}

		// Edit, sending empty cli context so that the flags are not set
		selected, err = prompts.SelectARepository(&cli.Context{}, selected)
		if err != nil {
			return err
		}

		pterm.Success.Printfln("Selected Repository: `%#v`", selected)
		return nil
	},
}
