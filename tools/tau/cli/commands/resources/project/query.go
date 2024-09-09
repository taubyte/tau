package project

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	projectPrompts "github.com/taubyte/tau/tools/tau/prompts/project"
	"github.com/taubyte/tau/tools/tau/prompts/spinner"
	projectTable "github.com/taubyte/tau/tools/tau/table/project"
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

	// If --select is set we should not check the user's currently selected project
	checkEnv := !ctx.Bool(flags.Select.Name)

	project, err := projectPrompts.GetOrSelect(ctx, checkEnv)
	if err != nil {
		return err
	}
	stopGlobe := spinner.Globe()
	repos, err := project.Repositories()
	if err != nil {
		return projectI18n.GettingRepositoriesFailed(project.Name, err)
	}

	description := projectLib.Description(project)
	stopGlobe()

	projectTable.Query(project, repos, description)

	return nil
}
