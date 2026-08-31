package fixtures

import (
	"github.com/taubyte/tau/legacy/schema/project"
)

func Project() (project.Project, error) {
	fs, err := VirtualFSWithBuiltProject()
	if err != nil {
		return nil, err
	}

	return project.Open(project.VirtualFS(fs, "/test_project/config"))
}
