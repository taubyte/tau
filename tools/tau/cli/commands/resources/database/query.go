package database

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	databaseLib "github.com/taubyte/tau/tools/tau/lib/database"
	databasePrompts "github.com/taubyte/tau/tools/tau/prompts/database"
	databaseTable "github.com/taubyte/tau/tools/tau/table/database"
)

func (link) Query() common.Command {
	return (&resources.Query[*structureSpec.Database]{
		LibListResources:   databaseLib.ListResources,
		TableList:          databaseTable.List,
		PromptsGetOrSelect: databasePrompts.GetOrSelect,
		TableQuery:         databaseTable.Query,
	}).Default()
}

func (link) List() common.Command {
	return (&resources.List[*structureSpec.Database]{
		LibListResources: databaseLib.ListResources,
		TableList:        databaseTable.List,
	}).Default()
}
