package databaseTable

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
)

func Query(database *structureSpec.Database) {
	prompts.RenderTable(getTableData(database, true))
}
