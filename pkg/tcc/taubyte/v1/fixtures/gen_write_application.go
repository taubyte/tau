package fixtures

import (
	appSchema "github.com/taubyte/tau/pkg/schema/application"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

func writeApplication(name string, project projectSchema.Project) error {
	app, err := project.Application(name)
	if err != nil {
		return err
	}

	return app.Set(
		true,
		appSchema.Id("QmZvW43kx7p8v5dZ1qV8WFtxtBnJA6Cr6pcZXp6p4L9kC3"),
		appSchema.Description("some app description"),
		appSchema.Tags([]string{"tag1", "tag2"}),
	)
}
