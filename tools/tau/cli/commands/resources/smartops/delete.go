package smartops

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	smartopsI18n "github.com/taubyte/tau/tools/tau/i18n/smartops"
	smartopsLib "github.com/taubyte/tau/tools/tau/lib/smartops"
	smartopsPrompts "github.com/taubyte/tau/tools/tau/prompts/smartops"
	smartopsTable "github.com/taubyte/tau/tools/tau/table/smartops"
)

func (link) Delete() common.Command {
	return (&resources.Delete[*structureSpec.SmartOp]{
		PromptsGetOrSelect: smartopsPrompts.GetOrSelect,
		TableConfirm:       smartopsTable.Confirm,
		PromptsDeleteThis:  smartopsPrompts.DeleteThis,
		LibDelete:          smartopsLib.Delete,
		I18nDeleted:        smartopsI18n.Deleted,
	}).Default()
}
