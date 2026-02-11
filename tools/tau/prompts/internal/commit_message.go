package main

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
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
		message, err := prompts.GetOrRequireACommitMessage(ctx)
		if err != nil {
			return err
		}

		printer.Out.SuccessPrintfln("Got commit message: `%s`", message)
		return nil
	},
}
