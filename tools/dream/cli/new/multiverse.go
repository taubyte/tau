package new

import (
	"github.com/pterm/pterm"
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	spec "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func multiverse(multiverse *client.Client) *cli.Command {
	return &cli.Command{
		Name: "multiverse",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "daemon",
				Usage: "Runs multiverse in background",
			},
			&cli.StringSliceFlag{
				Name:        "universes",
				Aliases:     []string{"u"},
				Usage:       "List universes separated by comma",
				DefaultText: "A single universe named 'blackhole'",
			},
			&cli.StringFlag{
				Name:        "id",
				DefaultText: "Id of a universe to load",
			},
			&cli.BoolFlag{
				Name:        "keep",
				DefaultText: "If set will store the universe in $HOME/.cache/dreamland rather than /tmp",
			},

			// Relative to the universes
			&cli.BoolFlag{
				Name:  "empty",
				Usage: "Create an empty multiverse (Overrides the below)",
			},
			&cli.BoolFlag{
				Name:    "listen-on-all",
				Aliases: []string{"L"},
				Usage:   "hosts dreamland http clients on 0.0.0.0 rather than 127.0.0.1",
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
			&cli.StringFlag{
				Name:        "branch",
				Usage:       "Set branch",
				DefaultText: spec.DefaultBranches[0],
				Value:       spec.DefaultBranches[0],
				Aliases:     []string{"b"},
			},
		},
		Action: runMultiverse(multiverse),
	}
}

func runMultiverse(multiverse *client.Client) cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		// TODO this is ugly, and we should be able to start a universe on a specific branch
		spec.DefaultBranches = []string{c.String("branch")}

		if c.Bool("listen-on-all") {
			dream.DefaultHost = "0.0.0.0"
		}

		// Set default universe name if no names provided
		universes := c.StringSlice("universes")
		if len(universes) == 0 {
			err = c.Set("universes", common.DefaultUniverseName)
			if err != nil {
				return err
			}
		}

		if c.Bool("empty") {
			err = api.BigBang()
			if err != nil {
				return err
			}

			err = startEmptyUniverses(c)
			if err != nil {
				return err
			}

			greatSuccess(c)
			if c.Bool("daemon") {
				common.DoDaemon = true
			} else {
				<-c.Done()
			}

			return
		}

		// Set default simple name if no names provided
		simples := c.StringSlice("simples")
		if len(simples) == 0 {
			err = c.Set("simples", common.DefaultClientName)
			if err != nil {
				return err
			}
		}

		// Start API
		err = api.BigBang()
		if err != nil {
			return err
		}

		// Start each universe
		err = startUniverses(c)
		if err != nil {
			return err
		}

		// Run fixtures
		err = runFixtures(c, multiverse, c.StringSlice("universes"))
		if err != nil {
			return err
		}

		greatSuccess(c)
		if c.Bool("daemon") {
			common.DoDaemon = true
		} else {
			<-c.Done()
		}

		return
	}
}

func greatSuccess(c *cli.Context) {
	universes := c.StringSlice("universes")
	if len(universes) == 1 {
		pterm.Success.Printf("Universe %s started!\n", universes[0])
	} else {
		pterm.Success.Printf("Multiverse containing %v started!\n", universes)
	}
}
