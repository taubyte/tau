package new

import (
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func Command(ctx *common.Context) *cli.Command {
	return &cli.Command{
		Name: "new",
		Subcommands: []*cli.Command{
			multiverse(ctx.Multiverse),
			universe(ctx.Multiverse),
		},
	}
}
