package project

import (
	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectFlags "github.com/taubyte/tau/tools/tau/flags/project"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/prompts"
	projectPrompts "github.com/taubyte/tau/tools/tau/prompts/project"
	"github.com/urfave/cli/v2"
)

func (link) Checkout() common.Command {
	return common.Create(&cli.Command{
		Flags: []cli.Flag{
			flags.Branch,
			projectFlags.ConfigOnly,
			projectFlags.CodeOnly,
		},
		Action: checkout,
	})
}

func checkout(ctx *cli.Context) error {
	project, err := projectPrompts.GetOrSelect(ctx, true)
	if err != nil {
		return err
	}

	repoHandler, err := projectLib.Repository(project.Name).Open()
	if err != nil {
		return err
	}

	configRepo, err := repoHandler.Config()
	if err != nil {
		return err
	}

	branch, err := prompts.SelectABranch(ctx, configRepo)
	if err != nil {
		return err
	}

	err = (&dualRepoHandler{
		ctx:         ctx,
		repository:  repoHandler,
		projectName: project.Name,
		errorFormat: projectI18n.CheckingOutProjectFailed,
		action: func(r *git.Repository) error {
			return r.Checkout(branch)
		},
	}).Run()
	if err != nil {
		return err
	}

	projectI18n.CheckedOutProject(project.Name, branch)
	return nil
}
