package flags

import "github.com/urfave/cli/v2"

var (
	CommitMessage = &cli.StringFlag{
		Name:    "message",
		Aliases: []string{"m"},
		Usage:   "Commit message for a git commit",
	}
)
