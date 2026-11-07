package new

import (
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func Command(ctx *common.Context) *cli.Command {
	return &cli.Command{
		Name:     "new",
		Usage:    "Create something in the running Dream (a universe)",
		Category: "Start & stop",
		Subcommands: []*cli.Command{
			//multiverse(ctx.Multiverse),
			universe(ctx.Multiverse),
		},
	}
}
