package service

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	serviceI18n "github.com/taubyte/tau/tools/tau/i18n/service"
	serviceLib "github.com/taubyte/tau/tools/tau/lib/service"
	servicePrompts "github.com/taubyte/tau/tools/tau/prompts/service"
	serviceTable "github.com/taubyte/tau/tools/tau/table/service"
)

func (link) Delete() common.Command {
	return (&resources.Delete[*structureSpec.Service]{
		PromptsGetOrSelect: servicePrompts.GetOrSelect,
		TableConfirm:       serviceTable.Confirm,
		PromptsDeleteThis:  servicePrompts.DeleteThis,
		LibDelete:          serviceLib.Delete,
		I18nDeleted:        serviceI18n.Deleted,
	}).Default()
}
