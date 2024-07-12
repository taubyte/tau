package status

import (
	"github.com/pterm/pterm"
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/dream/api"
	"github.com/taubyte/tau/tools/dream/cli/command"
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func getID(multiverse *client.Client) *cli.Command {
	c := &cli.Command{
		Name:   "id",
		Action: getIDStatus(multiverse),
	}
	command.NameWithDefault(c, common.DefaultUniverseName)

	return c
}

func getIDStatus(multiverse *client.Client) cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		var id api.UniverseInfo
		id, err = multiverse.Universe(c.String("name")).Id()
		if err != nil {
			return
		}
		pterm.Success.Printf("Universe id: %s\n", id.Id)

		return
	}
}
