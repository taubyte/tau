package projectFlags

import "github.com/urfave/cli/v2"

var Private = &cli.BoolFlag{
	Name:  "private",
	Usage: "Private config and code repositories",
}

var Public = &cli.BoolFlag{
	Name:  "public",
	Usage: "Public config and code repositories",
}
