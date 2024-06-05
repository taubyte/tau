package kill

import (
	"strings"

	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/tools/dream/cli/command"
	"github.com/urfave/cli/v2"
)

func services(multiverse *client.Client) *cli.Command {
	c := &cli.Command{
		Name:   "services",
		Action: killServices(multiverse),
	}

	command.Names(c)
	command.Universe(c)

	return c
}

func killServices(multiverse *client.Client) cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		universe := multiverse.Universe(c.String("universe"))
		services := strings.Split(c.String("names"), ",")

		for _, service := range services {
			err = universe.KillService(service)
			if err != nil {
				return err
			}
		}

		return
	}
}
