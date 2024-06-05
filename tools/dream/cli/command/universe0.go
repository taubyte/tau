package command

import (
	"errors"
	"strings"

	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/taubyte/tau/tools/dream/cli/flags"
	"github.com/urfave/cli/v2"
)

func Universe0(c *cli.Command) {
	c.Flags = append(c.Flags, &flags.Universe)

	if len(c.ArgsUsage) == 0 {
		c.ArgsUsage = "universe"
	} else {
		c.ArgsUsage = "universe," + c.ArgsUsage
	}

	action := c.Action

	c.Action = func(ctx *cli.Context) error {
		universe, err := getUniverse0(ctx)
		if err != nil {
			return err
		}
		ctx.Set("universe", universe)
		return action(ctx)
	}
}

// get the universe from the flag or the first argument
func getUniverse0(c *cli.Context) (universe string, err error) {
	universe = c.String("universe")
	if universe == common.DefaultUniverseName {
		args1 := c.Args().First()
		if len(args1) != 0 {
			universe = c.Args().First()
			if strings.HasPrefix(universe, "-") {
				err = errors.New("Parse arguments failed: write [arguments] after -flags")
				return
			}

		}
	}

	return
}
