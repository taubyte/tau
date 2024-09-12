package repositoryCommands

import resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"

func InitCommand(new *LibCommands) Commands {
	resources.PanicIfMissingValue(new)

	return &repositoryCommands{new}
}
