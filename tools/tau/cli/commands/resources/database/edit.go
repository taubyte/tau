package database

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	databaseFlags "github.com/taubyte/tau/tools/tau/flags/database"
	databaseI18n "github.com/taubyte/tau/tools/tau/i18n/database"
	databaseLib "github.com/taubyte/tau/tools/tau/lib/database"
	databasePrompts "github.com/taubyte/tau/tools/tau/prompts/database"
	databaseTable "github.com/taubyte/tau/tools/tau/table/database"
)

func (link) Edit() common.Command {
	return (&resources.Edit[*structureSpec.Database]{
		PromptsGetOrSelect: databasePrompts.GetOrSelect,
		PromptsEdit:        databasePrompts.Edit,
		TableConfirm:       databaseTable.Confirm,
		PromptsEditThis:    databasePrompts.EditThis,
		LibSet:             databaseLib.Set,
		I18nEdited:         databaseI18n.Edited,

		UniqueFlags: flags.Combine(
			flags.MatchRegex,
			flags.Match,
			flags.Local,
			databaseFlags.Encryption,
			databaseFlags.EncryptionKey,
			databaseFlags.Min,
			databaseFlags.Max,
			flags.Size,
			flags.SizeUnit,
		),
	}).Default()
}
