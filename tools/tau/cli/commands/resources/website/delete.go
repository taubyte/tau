package website

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	websiteI18n "github.com/taubyte/tau/tools/tau/i18n/website"
	websiteLib "github.com/taubyte/tau/tools/tau/lib/website"
	websitePrompts "github.com/taubyte/tau/tools/tau/prompts/website"
	websiteTable "github.com/taubyte/tau/tools/tau/table/website"
)

func (link) Delete() common.Command {
	return (&resources.Delete[*structureSpec.Website]{
		PromptsGetOrSelect: websitePrompts.GetOrSelect,
		TableConfirm:       websiteTable.Confirm,
		PromptsDeleteThis:  websitePrompts.DeleteThis,
		LibDelete:          websiteLib.Delete,
		I18nDeleted:        websiteI18n.Deleted,
	}).Default()
}
