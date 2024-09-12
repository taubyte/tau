package function

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	functionLib "github.com/taubyte/tau/tools/tau/lib/function"
	functionPrompts "github.com/taubyte/tau/tools/tau/prompts/function"
	functionTable "github.com/taubyte/tau/tools/tau/table/function"
)

func (link) Query() common.Command {
	return (&resources.Query[*structureSpec.Function]{
		LibListResources:   functionLib.ListResources,
		TableList:          functionTable.List,
		PromptsGetOrSelect: functionPrompts.GetOrSelect,
		TableQuery:         functionTable.Query,
	}).Default()
}

func (link) List() common.Command {
	return (&resources.List[*structureSpec.Function]{
		LibListResources: functionLib.ListResources,
		TableList:        functionTable.List,
	}).Default()
}
