package kill

import (
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func Command(ctx *common.Context) *cli.Command {
	commands := []*cli.Command{
		// multiverse(ctx.Multiverse), TODO
		simple(ctx.Multiverse),
		services(ctx.Multiverse),
		universe(ctx.Multiverse),
	}
	commands = append(commands, service(ctx.Multiverse)...)

	return &cli.Command{
		Name:        "kill",
		Subcommands: commands,
	}
}
