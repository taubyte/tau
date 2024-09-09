package library

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	libraryI18n "github.com/taubyte/tau/tools/tau/i18n/library"
	libraryLib "github.com/taubyte/tau/tools/tau/lib/library"
	libraryPrompts "github.com/taubyte/tau/tools/tau/prompts/library"
	libraryTable "github.com/taubyte/tau/tools/tau/table/library"
)

func (link) Delete() common.Command {
	return (&resources.Delete[*structureSpec.Library]{
		PromptsGetOrSelect: libraryPrompts.GetOrSelect,
		TableConfirm:       libraryTable.Confirm,
		PromptsDeleteThis:  libraryPrompts.DeleteThis,
		LibDelete:          libraryLib.Delete,
		I18nDeleted:        libraryI18n.Deleted,
	}).Default()
}
