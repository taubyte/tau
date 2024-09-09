package website

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

func (l link) New() common.Command {
	return common.Create(
		&cli.Command{
			Flags: flags.Combine(
				flags.Description,
				flags.Tags,
				flags.Template,

				flags.Domains,
				flags.Paths,
				flags.Provider,

				flags.RepositoryName,
				flags.RepositoryId,

				flags.Branch,

				flags.Clone,
				flags.EmbedToken,

				flags.GenerateRepo,
				flags.Private,

				flags.Yes,
			),
			Action: l.cmd.New,
		},
	)
}
