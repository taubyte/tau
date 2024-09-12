package domainTable

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
)

func Query(domain *structureSpec.Domain) {
	prompts.RenderTable(getTableData(domain, true))
}
