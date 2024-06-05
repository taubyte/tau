package new

import (
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/tools/dream/cli/command"
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func universe(multiverse *client.Client) *cli.Command {
	c := &cli.Command{
		Name: "universe",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "empty",
				Usage: "Create an empty universe (Overrides the below)",
			},
			&cli.StringSliceFlag{
				Name:  "enable",
				Usage: "List services separated by comma ( Conflicts with disable )",
			},
			&cli.StringSliceFlag{
				Name:  "disable",
				Usage: "List services separated by comma ( Conflicts with enable )",
			},
			&cli.StringSliceFlag{
				Name:  "bind",
				Usage: "service@0000/http,...,service@0000/p2p,...",
			},
			&cli.StringSliceFlag{
				Name:  "fixtures",
				Usage: "List fixtures separated by comma",
			},
			&cli.StringSliceFlag{
				Name:        "simples",
				Usage:       "List simples separated by comma",
				DefaultText: "Creates a simple named `client` with all clients",
			},
		},
		Action: runUniverse(multiverse),
	}

	// attach gets
	command.NameWithDefault(c, common.DefaultUniverseName)

	return c
}

func runUniverse(multiverse *client.Client) cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		if c.Bool("empty") {
			err = multiverse.StartUniverseWithConfig(c.String("name"), &dream.Config{})
			if err != nil {
				return err
			}
		}

		// Set default simple name if no names provided
		simples := c.StringSlice("simples")
		if len(simples) == 0 {
			err = c.Set("simples", common.DefaultClientName)
			if err != nil {
				return err
			}
		}

		config, err := buildConfig(c)
		if err != nil {
			return err
		}

		err = multiverse.StartUniverseWithConfig(c.String("name"), config)
		if err != nil {
			return err
		}

		// Run fixtures
		err = runFixtures(c, multiverse, []string{c.String("name")})
		if err != nil {
			return err
		}

		return
	}
}
