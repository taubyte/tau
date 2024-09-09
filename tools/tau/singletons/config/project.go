package config

import (
	"path"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/tools/tau/common"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/singletons"
)

func (p Project) ConfigLoc() (dir string) {
	return path.Join(p.Location, common.ConfigRepoDir)
}

func (p Project) CodeLoc() (dir string) {
	return path.Join(p.Location, common.CodeRepoDir)
}

func (p Project) WebsiteLoc() (dir string) {
	return path.Join(p.Location, common.WebsiteRepoDir)
}

func (p Project) LibraryLoc() (dir string) {
	return path.Join(p.Location, common.LibraryRepoDir)
}

func (p Project) Interface() (projectSchema.Project, error) {
	if len(p.Location) == 0 {
		return nil, singletonsI18n.ProjectLocationNotFound(p.Name)
	}

	schema, err := projectSchema.Open(projectSchema.SystemFS(p.ConfigLoc()))
	if err != nil {
		return nil, singletonsI18n.OpeningProjectConfigFailed(p.Location, err)
	}

	return schema, nil
}
