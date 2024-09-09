package website

import (
	repositoryCommands "github.com/taubyte/tau/tools/tau/cli/commands/resources/repository"
	"github.com/taubyte/tau/tools/tau/cli/common"
	websiteI18n "github.com/taubyte/tau/tools/tau/i18n/website"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	websiteLib "github.com/taubyte/tau/tools/tau/lib/website"
	websitePrompts "github.com/taubyte/tau/tools/tau/prompts/website"
	websiteTable "github.com/taubyte/tau/tools/tau/table/website"
	"github.com/urfave/cli/v2"
)

type link struct {
	common.UnimplementedBasic
	cmd repositoryCommands.Commands
}

// New is called in tau/cli/new.go to attach the relative commands
// to their parents, i.e `new` => `new website`
func New() common.Basic {
	l := link{}

	l.cmd = repositoryCommands.InitCommand(&repositoryCommands.LibCommands{
		Type:           repositoryLib.WebsiteRepositoryType,
		I18nCreated:    websiteI18n.Created,
		I18nEdited:     websiteI18n.Edited,
		I18nCheckedOut: websiteI18n.CheckedOut,
		I18nPulled:     websiteI18n.Pulled,
		I18nPushed:     websiteI18n.Pushed,
		I18nRegistered: websiteI18n.Registered,

		PromptsCreateThis: websitePrompts.CreateThis,
		PromptsEditThis:   websitePrompts.EditThis,

		PromptNew: func(ctx *cli.Context) (interface{}, repositoryCommands.Resource, error) {
			iface, resource, err := websitePrompts.New(ctx)
			return iface, Wrap(resource), err
		},
		PromptsEdit: func(ctx *cli.Context, resource repositoryCommands.Resource) (interface{}, error) {
			return websitePrompts.Edit(ctx, resource.(wrapped).UnWrap())
		},
		PromptsGetOrSelect: func(ctx *cli.Context) (repositoryCommands.Resource, error) {
			resource, err := websitePrompts.GetOrSelect(ctx)
			return Wrap(resource), err
		},
		LibNew: func(resource repositoryCommands.Resource) error {
			return websiteLib.New(resource.(wrapped).UnWrap())
		},
		LibSet: func(resource repositoryCommands.Resource) error {
			return websiteLib.Set(resource.(wrapped).UnWrap())
		},
		TableConfirm: func(ctx *cli.Context, resource repositoryCommands.Resource, prompt string) bool {
			return websiteTable.Confirm(ctx, resource.(wrapped).UnWrap(), prompt)
		},
	})

	return l
}
