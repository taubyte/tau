package functionTable

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
)

func Query(function *structureSpec.Function) {
	prompts.RenderTable(getTableData(function, true))
}
