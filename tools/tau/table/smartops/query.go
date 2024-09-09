package smartopsTable

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
)

func Query(smartops *structureSpec.SmartOp) {
	prompts.RenderTable(getTableData(smartops, true))
}
