package databasePrompts

const (
	NamePrompt          = "Database Name:"
	SelectPrompt        = "Select a Database:"
	DatabaseMatch       = "Match:"
	EncryptionPrompt    = "Encrypt Database:"
	EncryptionKeyPrompt = "Encryption Key:"
	MinPrompt           = "Minimum Replicas:"
	MaxPrompt           = "Maximum Replicas:"

	CreateThis                = "Create this database?"
	DeleteThis                = "Delete this database?"
	EditThis                  = "Edit this database?"
	NoneFound                 = "no databases found"
	NotFound                  = "database `%s` not found"
	ParsingMinFailed          = "parsing min(%s) as int failed with: %s"
	ParsingMaxFailed          = "parsing max(%s) as int failed with: %s"
	MinCannotBeGreaterThanMax = "min(%s) cannot be greater than max(%s)"
)
