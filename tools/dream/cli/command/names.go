package command

import (
	"github.com/taubyte/tau/tools/dream/cli/flags"
	"github.com/urfave/cli/v2"
)

func Names(c *cli.Command) {
	attachNames(c, &flags.Names)
}

func attachNames(c *cli.Command, flag cli.Flag) {
	c.Flags = append(c.Flags, flag)

	if len(c.ArgsUsage) == 0 {
		c.ArgsUsage = "[name,...]"
	} else {
		c.ArgsUsage = "[name,...]" + c.ArgsUsage
	}

	action := c.Action

	c.Action = func(ctx *cli.Context) error {
		names, err := getName(ctx)
		if err != nil {
			return err
		}
		ctx.Set("names", names)
		return action(ctx)
	}
}
