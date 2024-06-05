package kill

import (
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/tools/dream/cli/command"
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func simple(multiverse *client.Client) *cli.Command {
	c := &cli.Command{
		Name:   "simple",
		Action: killSimple(multiverse),
	}

	// Attach gets
	command.NameWithDefault(c, common.DefaultClientName)
	command.Universe(c)

	return c
}

func killSimple(multiverse *client.Client) cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		universe := multiverse.Universe(c.String("universe"))
		return universe.KillSimple(c.String("name"))
	}
}
