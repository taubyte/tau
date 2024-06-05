package kill

import (
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/tools/dream/cli/command"
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func universe(multiverse *client.Client) *cli.Command {
	c := &cli.Command{
		Name:   "universe",
		Action: killUniverse(multiverse),
	}

	command.NameWithDefault(c, common.DefaultUniverseName)

	return c
}

func killUniverse(multiverse *client.Client) cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		return multiverse.Universe(c.String("name")).Kill()
	}
}
