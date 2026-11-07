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
		Name:     "inject",
		Usage:    "Add nodes to a universe, or trigger builds as if GitHub had pushed",
		Category: "Build",
		Description: `Flags go BEFORE the universe name (default universe: "default").

Examples:
   dream inject push-all --force true
   dream inject push-all blackhole
   dream inject push-specific --rid 123456 --fn user/tb_code_app blackhole`,
		Subcommands: commands,
	}
}
