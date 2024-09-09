package repositoryCommands

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func (lib *repositoryCommands) PushCmd() common.Command {
	return common.Create(
		&cli.Command{
			Flags: []cli.Flag{
				flags.CommitMessage,
			},
			Action: lib.Push,
		},
	)
}

func (lib *repositoryCommands) Push(ctx *cli.Context) error {
	project, resource, info, err := lib.selectResource(ctx)
	if err != nil {
		return err
	}

	message := prompts.GetOrRequireACommitMessage(ctx)

	_, err = info.Push(project, message, resource.Get().RepositoryURL())
	if err != nil {
		return err
	}
	lib.I18nPushed(resource.Get().RepositoryURL(), message)

	return nil
}
