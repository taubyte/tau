package inject

import (
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/clients/http/dream/inject"
	"github.com/taubyte/tau/core/common"
	specs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/tools/dream/cli/command"

	"github.com/urfave/cli/v2"
)

func service(multiverse *client.Client) []*cli.Command {
	validServices := specs.Services
	commands := make([]*cli.Command, len(validServices))

	for idx, _service := range validServices {
		c := &cli.Command{
			Name: _service,
			Flags: []cli.Flag{
				&cli.IntFlag{
					Name: "http",
				},
			},
			Action: runService(_service, multiverse),
		}
		command.Universe0(c)
		commands[idx] = c
	}

	return commands
}

func runService(name string, multiverse *client.Client) cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		universe := multiverse.Universe(c.String("universe"))

		others := make(map[string]int, 0)
		http := c.Int("http")
		if http != 0 {
			others["http"] = http
		}

		config := &common.ServiceConfig{
			Others: others,
		}

		return universe.Inject(inject.Service(name, config))
	}
}
