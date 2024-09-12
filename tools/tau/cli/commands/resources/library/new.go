package library

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	libraryFlags "github.com/taubyte/tau/tools/tau/flags/library"
	"github.com/urfave/cli/v2"
)

func (l link) New() common.Command {
	return common.Create(
		&cli.Command{
			Flags: flags.Combine(
				flags.Description,
				flags.Tags,
				flags.Template,

				flags.Provider,
				flags.Path,

				flags.RepositoryName,
				flags.RepositoryId,
				flags.Clone,
				flags.EmbedToken,
				libraryFlags.GenerateRepo,
				flags.Private,
				flags.Branch,

				flags.Yes,
			),
			Action: l.cmd.New,
		},
	)
}
