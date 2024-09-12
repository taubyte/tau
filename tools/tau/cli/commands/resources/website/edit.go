package website

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

func (l link) Edit() common.Command {
	return common.Create(
		&cli.Command{
			Flags: flags.Combine(
				flags.Description,
				flags.Tags,

				flags.Domains,
				flags.Paths,

				flags.RepositoryName,
				flags.RepositoryId,

				flags.Branch,

				flags.Clone,
				flags.EmbedToken,

				// TODO maybe, handle generating a new repo

				flags.Yes,
			),
			Action: l.cmd.Edit,
		},
	)
}
