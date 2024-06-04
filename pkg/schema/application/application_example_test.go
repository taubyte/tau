package application_test

import (
	"fmt"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/schema/application"
	"github.com/taubyte/tau/pkg/schema/project"
)

func ExampleApplication() {
	// Create a new file system in memory
	fs := afero.NewMemMapFs()

	// Open a new project with a virtual file system
	project, err := project.Open(project.VirtualFS(fs, "/"))
	if err != nil {
		return
	}

	// Create or open an application
	app, err := project.Application("test_app")
	if err != nil {
		return
	}

	// Set and write application fields
	err = app.Set(true,
		application.Id("123456"),
		application.Description("this is an application"),
		application.Tags([]string{"tag1", "tag2"}),
	)
	if err != nil {
		return
	}

	// Display the Description
	fmt.Println(app.Get().Description())

	// Open the config.yaml of the application
	config, err := afero.ReadFile(fs, "/applications/test_app/config.yaml")
	if err != nil {
		return
	}

	// Print config.yaml
	fmt.Println(string(config))

	// Output: this is an application
	// id: "123456"
	// description: this is an application
	// tags:
	//     - tag1
	//     - tag2
}
