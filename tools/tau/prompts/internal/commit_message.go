package main

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var CommitMessage = &cli.Command{
	Name: "commit_message",
	Flags: []cli.Flag{
		flags.CommitMessage,
	},
	Action: func(ctx *cli.Context) error {

		// New
		message := prompts.GetOrRequireACommitMessage(ctx)

		pterm.Success.Printfln("Got commit message: `%s`", message)
		return nil
	},
}
