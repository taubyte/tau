package storageTable

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
)

func Query(storage *structureSpec.Storage) {
	prompts.RenderTable(getTableData(storage, true))
}
