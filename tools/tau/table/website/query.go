package websiteTable

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
)

func Query(website *structureSpec.Website) {
	prompts.RenderTable(getTableData(website, true))
}
