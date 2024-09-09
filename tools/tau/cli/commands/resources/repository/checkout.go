package repositoryCommands

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func (lib *repositoryCommands) CheckoutCmd() common.Command {
	return common.Create(
		&cli.Command{
			Flags: []cli.Flag{
				flags.Branch,
			},
			Action: lib.Checkout,
		},
	)
}

func (lib *repositoryCommands) Checkout(ctx *cli.Context) error {
	project, resource, info, err := lib.selectResource(ctx)
	if err != nil {
		return err
	}

	repo, err := info.Open(project, resource.Get().RepositoryURL())
	if err != nil {
		return err
	}

	branch, err := prompts.SelectABranch(ctx, repo)
	if err != nil {
		return err
	}

	err = repo.Checkout(branch)
	if err != nil {
		return err
	}
	lib.I18nCheckedOut(resource.Get().RepositoryURL(), branch)

	return nil
}
