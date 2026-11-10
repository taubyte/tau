package new

import (
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/tools/dream/cli/command"
	"github.com/urfave/cli/v2"
)

func universe(multiverse *client.Client) *cli.Command {
	c := &cli.Command{
		Name:  "universe",
		Usage: "Start a universe in the running Dream, with every service unless told otherwise",
		Description: `Starts a universe (default name: "default") in the running Dream. Its data is
kept across restarts. Put flags BEFORE the name.

Examples:
   dream new universe blackhole
   dream new universe --bind gateway@18080/http blackhole
   dream new universe --enable auth,tns,patrick --fresh test`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "empty",
				Usage: "First start the universe with no nodes, then apply the other flags",
			},
			&cli.BoolFlag{
				Name:  "fresh",
				Usage: "Delete the universe data and start it empty (still kept across restarts)",
			},
			&cli.StringSliceFlag{
				Name:  "enable",
				Usage: "Only start these services, comma separated (conflicts with --disable)",
			},
			&cli.StringSliceFlag{
				Name:  "disable",
				Usage: "Start every service except these, comma separated (conflicts with --enable)",
			},
			&cli.StringSliceFlag{
				Name:  "bind",
				Usage: "Fix a service port: service@port[/http|https|p2p|dns], comma separated",
			},
			&cli.StringSliceFlag{
				Name:  "fixtures",
				Usage: "Fixtures to run after the start, comma separated",
			},
			&cli.StringSliceFlag{
				Name:        "simples",
				Usage:       "Simple (client) nodes to add, comma separated",
				DefaultText: "one simple named client, with all clients",
			},
		},
		Action: runUniverse(multiverse),
	}

	// attach gets
	command.NameWithDefault(c, dream.DefaultUniverseName)

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
			err = c.Set("simples", dream.DefaultClientName)
			if err != nil {
				return err
			}
		}

		config, err := buildConfig(c)
		if err != nil {
			return err
		}

		if c.Bool("fresh") {
			err = multiverse.StartFreshUniverseWithConfig(c.String("name"), config)
		} else {
			err = multiverse.StartUniverseWithConfig(c.String("name"), config)
		}
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
