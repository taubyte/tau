package repositoryCommands

func InitCommand(new *LibCommands) Commands {
	PanicIfMissingValue(new)

	return &repositoryCommands{new}
}
