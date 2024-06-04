package common

import (
	"regexp"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

type Config struct {
	Branch                string
	Commit                string
	Provider              string
	RepositoryId          string
	Project               projectSchema.Project
	GeneratedDomainRegExp *regexp.Regexp
}
