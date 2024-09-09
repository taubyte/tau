package repositoryLib

import "github.com/taubyte/tau/tools/tau/singletons/templates"

type RepositoryType = string

const (
	WebsiteRepositoryType RepositoryType = "website"
	LibraryRepositoryType RepositoryType = "library"
)

type Info struct {
	FullName string
	ID       string

	Type RepositoryType

	DoClone bool
}

type InfoTemplate struct {
	RepositoryName string
	Info           templates.TemplateInfo
	Private        bool
}
