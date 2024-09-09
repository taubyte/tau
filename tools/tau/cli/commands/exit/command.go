package exit

import (
	"github.com/taubyte/tau/tools/tau/singletons/session"
	"github.com/urfave/cli/v2"
)

var Command = &cli.Command{
	Name:    "tau",
	Usage:   "Clears the current session",
	Aliases: []string{"exit"},
	Action:  Run,
}

func Run(c *cli.Context) error {
	return session.Delete()
}
