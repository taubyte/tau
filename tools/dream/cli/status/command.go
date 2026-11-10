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
		Name:     "status",
		Usage:    "Show the nodes, ports and id of a universe or service",
		Category: "Inspect",
		Description: `Examples:
   dream status universe blackhole
   dream status gateway blackhole
   dream status id blackhole`,
		Subcommands: commands,
	}
}
