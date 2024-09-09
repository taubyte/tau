package dreamLib

import (
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/tools/tau/singletons/config"
)

type internalJob func() error

type ProdProject struct {
	Project project.Project
	Profile config.Profile
}

type CompileForDFunc struct {
	ProjectId     string
	ApplicationId string
	ResourceId    string
	Branch        string
	Call          string
	Path          string
}

type CompileForRepository struct {
	ProjectId     string
	ApplicationId string
	ResourceId    string
	Branch        string
	Path          string
}
