package storage

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	storageI18n "github.com/taubyte/tau/tools/tau/i18n/storage"
	storageLib "github.com/taubyte/tau/tools/tau/lib/storage"
	storagePrompts "github.com/taubyte/tau/tools/tau/prompts/storage"
	storageTable "github.com/taubyte/tau/tools/tau/table/storage"
)

func (link) Delete() common.Command {
	return (&resources.Delete[*structureSpec.Storage]{
		PromptsGetOrSelect: storagePrompts.GetOrSelect,
		TableConfirm:       storageTable.Confirm,
		PromptsDeleteThis:  storagePrompts.DeleteThis,
		LibDelete:          storageLib.Delete,
		I18nDeleted:        storageI18n.Deleted,
	}).Default()
}
