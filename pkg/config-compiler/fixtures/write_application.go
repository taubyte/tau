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
		appSchema.Id("someappID"),
		appSchema.Description("some app description"),
		appSchema.Tags([]string{"tag1", "tag2"}),
	)
}
