package inject

import (
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func Command(ctx *common.Context) *cli.Command {
	commands := []*cli.Command{
		// TODO fixtures(ctx.Multiverse),
		services(ctx.Multiverse),
		simple(ctx.Multiverse),
	}
	commands = append(commands, service(ctx.Multiverse)...)
	commands = append(commands, fixture(ctx.Multiverse)...)
	return &cli.Command{
		Name:        "inject",
		Subcommands: commands,
	}
}
