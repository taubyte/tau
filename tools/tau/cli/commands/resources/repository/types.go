package repositoryCommands

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	"github.com/urfave/cli/v2"
)

type Getter interface {
	Name() string
	Description() string
	RepoName() string
	RepoID() string
	Branch() string
	RepositoryURL() string
}

type Setter interface {
	RepoID(string)
	RepoName(string)
}

type Resource interface {
	Get() Getter
	Set() Setter
}

type LibCommands struct {
	// New
	PromptNew         func(*cli.Context) (interface{}, Resource, error)
	LibNew            func(Resource) error
	I18nCreated       func(string)
	PromptsCreateThis string

	// Edit
	PromptsGetOrSelect func(*cli.Context) (Resource, error)
	PromptsEdit        func(*cli.Context, Resource) (interface{}, error)
	I18nEdited         func(string)
	PromptsEditThis    string

	// Repository
	I18nCheckedOut func(url string, branch string)
	I18nPulled     func(url string)
	I18nPushed     func(url string, commitMessage string)

	// Common
	Type           repositoryLib.RepositoryType
	LibSet         func(Resource) error
	TableConfirm   func(*cli.Context, Resource, string) bool
	I18nRegistered func(string)
}

type repositoryCommands struct {
	*LibCommands
}

type Commands interface {
	New(ctx *cli.Context) error
	Edit(ctx *cli.Context) error
	Import(ctx *cli.Context) error
	CheckoutCmd() common.Command
	CloneCmd() common.Command
	PushCmd() common.Command
	PullCmd() common.Command
}
