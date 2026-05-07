package projectFlags

import "github.com/urfave/cli/v2"

var Account = &cli.StringFlag{
	Name:  "account",
	Usage: "tau Account slug to pin the project to on the active profile's cloud (use with --plan)",
}

var Plan = &cli.StringFlag{
	Name:  "plan",
	Usage: "Plan slug within the chosen Account (use with --account)",
}
