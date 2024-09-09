package main

import (
	"log"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
)

var commands = []*cli.Command{
	MemoryCommand,
	CommitMessage,
	MultiSelectCommand,
	PathsCommand,
	BoolCommand,
	WebTokenCommand,
	TagsCommand,
	TagsRequiredCommand,
	TemplateCommand,
	DomainsCommand,
	SelectRepositoryCommand,
	SourceCommand,
	LanguageCommand,
	CallCommand,
	ServiceCommand,
}

func main() {
	app := cli.NewApp()
	app.Commands = commands

	// Trim suffix for simple autocomplete from relative files
	if len(os.Args) > 1 {
		os.Args[1] = strings.TrimSuffix(os.Args[1], ".go")
	}
	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
