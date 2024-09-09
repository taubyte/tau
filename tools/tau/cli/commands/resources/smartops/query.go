package smartops

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	smartopsLib "github.com/taubyte/tau/tools/tau/lib/smartops"
	smartopsPrompts "github.com/taubyte/tau/tools/tau/prompts/smartops"
	smartopsTable "github.com/taubyte/tau/tools/tau/table/smartops"
)

func (link) Query() common.Command {
	return (&resources.Query[*structureSpec.SmartOp]{
		LibListResources:   smartopsLib.ListResources,
		TableList:          smartopsTable.List,
		PromptsGetOrSelect: smartopsPrompts.GetOrSelect,
		TableQuery:         smartopsTable.Query,
	}).Default()
}

func (link) List() common.Command {
	return (&resources.List[*structureSpec.SmartOp]{
		LibListResources: smartopsLib.ListResources,
		TableList:        smartopsTable.List,
	}).Default()
}
