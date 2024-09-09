package messaging

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	messagingFlags "github.com/taubyte/tau/tools/tau/flags/messaging"
	messagingI18n "github.com/taubyte/tau/tools/tau/i18n/messaging"
	messagingLib "github.com/taubyte/tau/tools/tau/lib/messaging"
	messagingPrompts "github.com/taubyte/tau/tools/tau/prompts/messaging"
	messagingTable "github.com/taubyte/tau/tools/tau/table/messaging"
)

func (link) New() common.Command {
	return (&resources.New[*structureSpec.Messaging]{
		PromptsNew:        messagingPrompts.New,
		TableConfirm:      messagingTable.Confirm,
		PromptsCreateThis: messagingPrompts.CreateThis,
		LibNew:            messagingLib.New,
		I18nCreated:       messagingI18n.Created,

		UniqueFlags: flags.Combine(
			flags.Local,
			flags.MatchRegex,
			flags.Match,
			messagingFlags.MQTT,
			messagingFlags.WebSocket,
		),
	}).Default()
}
