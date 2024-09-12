package main

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

var TagsCommand = &cli.Command{
	Name: "tags",
	Flags: []cli.Flag{
		flags.Tags,
	},
	Action: func(ctx *cli.Context) error {
		tagsPrompt(ctx, false)

		return nil
	},
}

var TagsRequiredCommand = &cli.Command{
	Name: "tags-required",
	Flags: []cli.Flag{
		flags.Tags,
	},
	Action: func(ctx *cli.Context) error {
		tagsPrompt(ctx, true)

		return nil
	},
}

func tagsPrompt(ctx *cli.Context, required bool) {
	var tags []string
	if required {
		// New
		tags = prompts.RequiredTags(ctx)

		// Edit
		tags = prompts.RequiredTags(&cli.Context{}, tags)
	} else {
		// New
		tags = prompts.GetOrAskForTags(ctx)

		// Edit
		tags = prompts.GetOrAskForTags(&cli.Context{}, tags)
	}

	pterm.Success.Printfln("Got tags: `%v`", tags)
}
