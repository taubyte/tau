package inject

import (
	"errors"
	"fmt"

	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/clients/http/dream/inject"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	specs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/tools/dream/cli/command"
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func simple(multiverse *client.Client) *cli.Command {
	c := &cli.Command{
		Name: "simple",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:  "enable",
				Usage: "Starts a simple node with these clients enabled",
			},
			&cli.StringSliceFlag{
				Name:  "disable",
				Usage: "Starts a simple node with these clients disabled",
			},
			&cli.BoolFlag{
				Name:  "empty",
				Usage: "Starts an empty simple",
			},
		},
		Action: runSimple(multiverse),
	}

	command.NameWithDefault(c, common.DefaultClientName)
	command.Universe(c)

	return c
}

func runSimple(multiverse *client.Client) cli.ActionFunc {
	return func(c *cli.Context) (err error) {

		enabled := c.StringSlice("enable")
		disabled := c.StringSlice("disable")

		validClients := specs.P2PStreamServices

		config := &dream.SimpleConfig{
			Clients: make(map[string]*commonIface.ClientConfig),
		}
		if !c.Bool("empty") {
			if len(enabled) != 0 && len(disabled) != 0 {
				return errors.New("enable and disable flags cannot be paired")
			}

			// Add all valid clients
			if len(enabled) == 0 && len(disabled) == 0 {
				for _, client := range validClients {
					config.Clients[client] = &commonIface.ClientConfig{}
				}

				// Add only enabled clients
			} else if len(enabled) != 0 {
				err = checkClientsValid(validClients, enabled...)
				if err != nil {
					return err
				}

				for _, client := range enabled {
					config.Clients[client] = &commonIface.ClientConfig{}
				}

				// Add disabled clients
			} else /* if len(disabled) != 0 */ {
				err = checkClientsValid(validClients, disabled...)
				if err != nil {
					return err
				}

				enabled = make([]string, 0)
				for _, client := range validClients {
					_disabled := false
					for _, _client := range disabled {
						if client == _client {
							_disabled = true
						}
					}

					if !_disabled {
						enabled = append(enabled, client)
					}
				}

				for _, client := range enabled {
					config.Clients[client] = &commonIface.ClientConfig{}
				}
			}

		} else {
			if len(enabled) != 0 || len(disabled) != 0 {
				return errors.New("enable and disable are useless when creating empty")
			}
		}

		universe := multiverse.Universe(c.String("universe"))
		return universe.Inject(inject.Simple(c.String("name"), config))
	}
}

func checkClientsValid(validClients []string, clients ...string) error {
	for _, client := range clients {
		found := false
		for _, _client := range validClients {
			if client == _client {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("client `%s` not valid, should be one of %v", client, validClients)
		}
	}

	return nil
}
