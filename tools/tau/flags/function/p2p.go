package functionFlags

import "github.com/urfave/cli/v2"

var (
	Protocol = &cli.StringFlag{
		Name:     "protocol",
		Aliases:  []string{"pr"},
		Category: CategoryP2P,
		Usage:    "Protocol to use for the endpoint, either service name or protocol",
	}

	Command = &cli.StringFlag{
		Name:     "command",
		Aliases:  []string{"cmd"},
		Category: CategoryP2P,
		Usage:    "Command to execute",
	}
)

func P2P() []cli.Flag {
	return []cli.Flag{
		Protocol,
		Command,
	}
}
