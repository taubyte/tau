package application

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	applicationPrompts "github.com/taubyte/tau/tools/tau/prompts/application"
	applicationTable "github.com/taubyte/tau/tools/tau/table/application"
	"github.com/urfave/cli/v2"
)

func (link) Query() common.Command {
	return common.Create(
		&cli.Command{
			Flags: []cli.Flag{
				flags.List,
				flags.Select,
			},
			Action: query,
		},
	)
}

func (link) List() common.Command {
	return common.Create(
		&cli.Command{
			Action: list,
		},
	)
}

func query(ctx *cli.Context) error {
	if ctx.Bool(flags.List.Name) {
		return list(ctx)
	}

	// If --select is set we should not check the user's currently selected application
	checkEnv := !ctx.Bool(flags.Select.Name)

	application, err := applicationPrompts.GetOrSelect(ctx, checkEnv)
	if err != nil {
		return err
	}

	applicationTable.Query(application)

	return nil
}
