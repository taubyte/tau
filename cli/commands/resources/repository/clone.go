package repositoryCommands

import (
	"github.com/taubyte/tau/cli/common"
	"github.com/taubyte/tau/flags"
	"github.com/taubyte/tau/prompts"
	"github.com/urfave/cli/v2"
)

func (lib *repositoryCommands) CloneCmd() common.Command {
	return common.Create(
		&cli.Command{
			Flags: flags.Combine(
				flags.EmbedToken,
			),
			Action: lib.Clone,
		},
	)
}

func (lib *repositoryCommands) Clone(ctx *cli.Context) error {
	project, resource, info, err := lib.selectResource(ctx)
	if err != nil {
		return err
	}

	_, err = info.Clone(project, resource.Get().RepositoryURL(), resource.Get().Branch(), prompts.GetOrAskForEmbedToken(ctx))
	if err != nil {
		return err
	}

	return nil
}
