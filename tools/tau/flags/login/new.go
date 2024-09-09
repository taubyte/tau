package loginFlags

import "github.com/urfave/cli/v2"

var New = &cli.BoolFlag{
	Name:  "new",
	Usage: "Create a new profile. If no profiles exist, this flag is implied.",
}
