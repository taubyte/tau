package fixtures

import (
	"github.com/spf13/afero"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

func writeProject(fs afero.Fs) (projectSchema.Project, error) {
	project, err := projectSchema.Open(projectSchema.VirtualFS(fs, rootDir))
	if err != nil {
		return nil, err
	}

	err = project.Set(
		true,
		projectSchema.Id("QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"),
		projectSchema.Name(testProjectName),
		projectSchema.Description("Test Project"),
		projectSchema.Email("test@taubyte.com"),
	)
	if err != nil {
		return nil, err
	}
	return project, nil
}
