package function

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	functionFlags "github.com/taubyte/tau/tools/tau/flags/function"
	functionI18n "github.com/taubyte/tau/tools/tau/i18n/function"
	functionLib "github.com/taubyte/tau/tools/tau/lib/function"
	functionPrompts "github.com/taubyte/tau/tools/tau/prompts/function"
	functionTable "github.com/taubyte/tau/tools/tau/table/function"
)

func (link) Edit() common.Command {
	return (&resources.Edit[*structureSpec.Function]{
		PromptsGetOrSelect: functionPrompts.GetOrSelect,
		PromptsEdit:        functionPrompts.Edit,
		TableConfirm:       functionTable.Confirm,
		PromptsEditThis:    functionPrompts.EditThis,
		LibSet:             functionLib.Set,
		I18nEdited:         functionI18n.Edited,

		UniqueFlags: flags.Combine(
			flags.Timeout,
			flags.Memory,
			flags.MemoryUnit,
			functionFlags.Type,
			flags.Source,
			flags.Call,
			functionFlags.Http(),

			// P2P and PubSub
			flags.Local,
			functionFlags.P2P(),
			functionFlags.PubSub(),
		),
	}).Default()
}
