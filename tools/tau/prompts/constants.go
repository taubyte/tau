package prompts

import "errors"

// Errors
var (
	ErrorNoServicesDefined = errors.New("no services defined in project config")
	ErrorNoValidDomains    = errors.New("no valid domains")
)

const (
	// Prompts
	BranchPrompt        = "Branch:"
	CommitMessagePrompt = "Commit Message:"
	DescriptionPrompt   = "Description:"
	EntryPointPrompt    = "Entry Point:"

	MemoryPrompt           = "Memory:"
	PathsPrompt            = "Paths [comma separated]:"
	SizePrompt             = "Size:"
	TTLPrompt              = "TTL:"
	UnitTypePrompt         = "Unit type:"
	TrueSelect             = "True"
	TrueSelectL            = "true"
	FalseSelect            = "False"
	FalseSelectL           = "false"
	TagsPrompt             = "Tags [comma separated]:"
	RegexPrompt            = "Use Regex For Match:"
	LocalPrompt            = "Local:"
	GenerateRepoPrompt     = "Generate a Repository:"
	UseTemplatePrompt      = "Use a Template:"
	SelectTemplatePrompt   = "Select a Template:"
	RepositoryNamePrompt   = "Repository Name:"
	RepositorySelectPrompt = "Select a Repository"
	ClonePrompt            = "Clone this Repository"
	Domains                = "Domains:"
	EmbedTokenPrompt       = "Embed Git Token Into Clone URL:"
	BranchSelectPrompt     = "Select a Branch:"
	PrivatePrompt          = "Private:"
	SourcePrompt           = "Code source:"
	CodeLanguagePrompt     = "Code Language:"
	CallPrompt             = "Entry Point:"
	TimeoutPrompt          = "Time To Live:"

	CloudPrompts = "Cloud:"
	FQDN         = "FQDN:"
	Universe     = "Universe:"

	NoDomainGeneratePrompt = "No domains found, generate one?"

	// Error messages
	Required                   = "Required"
	MustBeABooleanValue        = "Must be a boolean value"
	FieldNotDefinedInConfig    = "field not defined in config: %#v"
	DoubleStringNotFound       = "%s %s not found"
	StringIsRequired           = "%s is required"
	StringIsNotAValidSelection = "`%s` not a valid selection: %v"
	InvalidSize                = "invalid size: %s Ex:(10, 10GB, 10PB)"
	SelectPromptNoOptions      = "no options to select from for prompt: %s"
	NoServiceFromFlag          = "unable to find service with selection: (--%s %s)"

	//    Device Errors
	TagLessThanThreeCharacters = "Tags cannot be less than three characters"
)

var (
	SelectionNone = "(none)"
)
