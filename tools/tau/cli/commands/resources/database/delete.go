package database

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	databaseI18n "github.com/taubyte/tau/tools/tau/i18n/database"
	databaseLib "github.com/taubyte/tau/tools/tau/lib/database"
	databasePrompts "github.com/taubyte/tau/tools/tau/prompts/database"
	databaseTable "github.com/taubyte/tau/tools/tau/table/database"
)

func (link) Delete() common.Command {
	return (&resources.Delete[*structureSpec.Database]{
		PromptsGetOrSelect: databasePrompts.GetOrSelect,
		TableConfirm:       databaseTable.Confirm,
		PromptsDeleteThis:  databasePrompts.DeleteThis,
		LibDelete:          databaseLib.Delete,
		I18nDeleted:        databaseI18n.Deleted,
	}).Default()
}
