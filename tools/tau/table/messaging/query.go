package messagingTable

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
)

func Query(messaging *structureSpec.Messaging) {
	prompts.RenderTable(getTableData(messaging, true))
}
