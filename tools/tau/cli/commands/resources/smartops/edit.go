package smartops

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	smartopsI18n "github.com/taubyte/tau/tools/tau/i18n/smartops"
	smartopsLib "github.com/taubyte/tau/tools/tau/lib/smartops"
	smartopsPrompts "github.com/taubyte/tau/tools/tau/prompts/smartops"
	smartopsTable "github.com/taubyte/tau/tools/tau/table/smartops"
)

func (link) Edit() common.Command {
	return (&resources.Edit[*structureSpec.SmartOp]{
		PromptsGetOrSelect: smartopsPrompts.GetOrSelect,
		PromptsEdit:        smartopsPrompts.Edit,
		TableConfirm:       smartopsTable.Confirm,
		PromptsEditThis:    smartopsPrompts.EditThis,
		LibSet:             smartopsLib.Set,
		I18nEdited:         smartopsI18n.Edited,

		UniqueFlags: flags.Combine(
			flags.Timeout,
			flags.Memory,
			flags.MemoryUnit,
			flags.Source,
			flags.Call,
		),
	}).Default()
}
