package library

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/urfave/cli/v2"
)

func (l link) Import() common.Command {
	return common.Create(
		&cli.Command{
			Action: l.cmd.Import,
		},
	)
}
