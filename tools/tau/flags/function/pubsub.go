package functionFlags

import "github.com/urfave/cli/v2"

var Channel = &cli.StringFlag{
	Name:     "channel",
	Aliases:  []string{"ch"},
	Category: CategoryPubSub,
	Usage:    "Channel to subscribe to",
}

func PubSub() []cli.Flag {
	return []cli.Flag{
		Channel,
	}
}
