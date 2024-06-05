package status

import (
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func Command(ctx *common.Context) *cli.Command {
	commands := []*cli.Command{
		// multiverse(ctx.Multiverse), TODO
		universe(ctx.Multiverse),
		getID(ctx.Multiverse),
	}
	commands = append(commands, service(ctx.Multiverse)...)

	return &cli.Command{
		Name:        "status",
		Subcommands: commands,
	}
}
