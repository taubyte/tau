package libraryTable

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
)

func Query(library *structureSpec.Library) {
	prompts.RenderTable(getTableData(library, true))
}
