package services_test

import (
	"fmt"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/pkg/schema/services"
)

func ExampleService() {
	// Create a new file system in memory
	fs := afero.NewMemMapFs()

	// Open a new project with a virtual file system
	project, err := project.Open(project.VirtualFS(fs, "/"))
	if err != nil {
		return
	}

	// Create or open an service
	srv, err := project.Service("test_srv", "")
	if err != nil {
		return
	}

	// Set and write service fields
	err = srv.Set(true,
		services.Id("QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc"),
		services.Description("a basic service"),
		services.Tags([]string{"tag1", "tag2"}),
		services.Protocol("/test/v1"),
	)
	if err != nil {
		return
	}

	// Display the Description
	fmt.Println(srv.Get().Description())

	// Open the config.yaml of the service
	config, err := afero.ReadFile(fs, "/services/test_srv.yaml")
	if err != nil {
		return
	}

	// Print config.yaml
	fmt.Println(string(config))

	// Output: a basic service
	// id: QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc
	// description: a basic service
	// tags:
	//     - tag1
	//     - tag2
	// protocol: /test/v1
}
