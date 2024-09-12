package function

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	functionI18n "github.com/taubyte/tau/tools/tau/i18n/function"
	functionLib "github.com/taubyte/tau/tools/tau/lib/function"
	functionPrompts "github.com/taubyte/tau/tools/tau/prompts/function"
	functionTable "github.com/taubyte/tau/tools/tau/table/function"
)

func (link) Delete() common.Command {
	return (&resources.Delete[*structureSpec.Function]{
		PromptsGetOrSelect: functionPrompts.GetOrSelect,
		TableConfirm:       functionTable.Confirm,
		PromptsDeleteThis:  functionPrompts.DeleteThis,
		LibDelete:          functionLib.Delete,
		I18nDeleted:        functionI18n.Deleted,
	}).Default()
}
