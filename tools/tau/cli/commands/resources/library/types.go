package library

import (
	repositoryCommands "github.com/taubyte/tau/tools/tau/cli/commands/resources/repository"
	"github.com/taubyte/tau/tools/tau/cli/common"
	libraryI18n "github.com/taubyte/tau/tools/tau/i18n/library"
	libraryLib "github.com/taubyte/tau/tools/tau/lib/library"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	libraryPrompts "github.com/taubyte/tau/tools/tau/prompts/library"
	libraryTable "github.com/taubyte/tau/tools/tau/table/library"
	"github.com/urfave/cli/v2"
)

type link struct {
	common.UnimplementedBasic
	cmd repositoryCommands.Commands
}

// New is called in tau/cli/new.go to attach the relative commands
// to their parents, i.e `new` => `new library`
func New() common.Basic {
	l := link{}

	l.cmd = repositoryCommands.InitCommand(&repositoryCommands.LibCommands{
		Type:           repositoryLib.LibraryRepositoryType,
		I18nCreated:    libraryI18n.Created,
		I18nEdited:     libraryI18n.Edited,
		I18nCheckedOut: libraryI18n.CheckedOut,
		I18nPulled:     libraryI18n.Pulled,
		I18nPushed:     libraryI18n.Pushed,
		I18nRegistered: libraryI18n.Registered,

		PromptsCreateThis: libraryPrompts.CreateThis,
		PromptsEditThis:   libraryPrompts.EditThis,

		PromptNew: func(ctx *cli.Context) (interface{}, repositoryCommands.Resource, error) {
			iface, resource, err := libraryPrompts.New(ctx)
			return iface, Wrap(resource), err
		},
		PromptsEdit: func(ctx *cli.Context, resource repositoryCommands.Resource) (interface{}, error) {
			return libraryPrompts.Edit(ctx, resource.(wrapped).UnWrap())
		},
		PromptsGetOrSelect: func(ctx *cli.Context) (repositoryCommands.Resource, error) {
			resource, err := libraryPrompts.GetOrSelect(ctx)
			return Wrap(resource), err
		},
		LibNew: func(resource repositoryCommands.Resource) error {
			return libraryLib.New(resource.(wrapped).UnWrap())
		},
		LibSet: func(resource repositoryCommands.Resource) error {
			return libraryLib.Set(resource.(wrapped).UnWrap())
		},
		TableConfirm: func(ctx *cli.Context, resource repositoryCommands.Resource, prompt string) bool {
			return libraryTable.Confirm(ctx, resource.(wrapped).UnWrap(), prompt)
		},
	})

	return l
}
