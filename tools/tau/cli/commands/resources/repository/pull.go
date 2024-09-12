package repositoryCommands

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/urfave/cli/v2"
)

func (lib *repositoryCommands) PullCmd() common.Command {
	return common.Create(
		&cli.Command{
			Action: lib.Pull,
		},
	)
}

func (lib *repositoryCommands) Pull(ctx *cli.Context) error {
	project, resource, info, err := lib.selectResource(ctx)
	if err != nil {
		return err
	}

	_, err = info.Pull(project, resource.Get().RepositoryURL())
	if err != nil {
		return err
	}
	lib.I18nPulled(resource.Get().RepositoryURL())

	return nil
}
