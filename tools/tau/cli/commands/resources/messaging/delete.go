package messaging

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	messagingI18n "github.com/taubyte/tau/tools/tau/i18n/messaging"
	messagingLib "github.com/taubyte/tau/tools/tau/lib/messaging"
	messagingPrompts "github.com/taubyte/tau/tools/tau/prompts/messaging"
	messagingTable "github.com/taubyte/tau/tools/tau/table/messaging"
)

func (link) Delete() common.Command {
	return (&resources.Delete[*structureSpec.Messaging]{
		PromptsGetOrSelect: messagingPrompts.GetOrSelect,
		TableConfirm:       messagingTable.Confirm,
		PromptsDeleteThis:  messagingPrompts.DeleteThis,
		LibDelete:          messagingLib.Delete,
		I18nDeleted:        messagingI18n.Deleted,
	}).Default()
}
