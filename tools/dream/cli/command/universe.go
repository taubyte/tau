package command

import (
	"errors"
	"log"
	"strings"

	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/taubyte/tau/tools/dream/cli/flags"
	"github.com/urfave/cli/v2"
)

func Universe(c *cli.Command) {
	c.Flags = append(c.Flags, &flags.Universe)

	if len(c.ArgsUsage) == 0 {
		log.Fatal("universe expected to be second argument")
	}

	c.ArgsUsage += ", universe"

	action := c.Action

	c.Action = func(ctx *cli.Context) error {
		universe, err := getUniverse(ctx)
		if err != nil {
			return err
		}
		ctx.Set("universe", universe)
		return action(ctx)
	}
}

// get the universe from the flag or the secound argument
func getUniverse(c *cli.Context) (universe string, err error) {
	universe = c.String("universe")
	if universe == common.DefaultUniverseName {
		args1 := c.Args().Get(1)
		if len(args1) != 0 {
			universe = c.Args().Get(1)
			if strings.HasPrefix(universe, "-") {
				err = errors.New("Parse arguments failed: write [arguments] after -flags")
				return
			}

		}
	}

	return
}
