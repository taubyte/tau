package config

import (
	"path"

	"github.com/taubyte/tau/tools/tau/common"
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
