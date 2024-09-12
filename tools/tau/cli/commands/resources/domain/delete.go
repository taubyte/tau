package domain

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	domainI18n "github.com/taubyte/tau/tools/tau/i18n/domain"
	domainLib "github.com/taubyte/tau/tools/tau/lib/domain"
	domainPrompts "github.com/taubyte/tau/tools/tau/prompts/domain"
	domainTable "github.com/taubyte/tau/tools/tau/table/domain"
)

func (link) Delete() common.Command {
	return (&resources.Delete[*structureSpec.Domain]{
		PromptsGetOrSelect: domainPrompts.GetOrSelect,
		TableConfirm:       domainTable.Confirm,
		PromptsDeleteThis:  domainPrompts.DeleteThis,
		LibDelete:          domainLib.Delete,
		I18nDeleted:        domainI18n.Deleted,
	}).Default()
}
