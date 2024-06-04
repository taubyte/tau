package storages_test

import (
	"fmt"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/pkg/schema/storages"
)

func ExampleStorage() {
	// Create a new file system in memory
	fs := afero.NewMemMapFs()

	// Open a new project with a virtual file system
	project, err := project.Open(project.VirtualFS(fs, "/"))
	if err != nil {
		return
	}

	// Create or open an storage
	stg, err := project.Storage("test_stg", "")
	if err != nil {
		return
	}

	// Set and write storage fields
	err = stg.Set(true,
		storages.Id("QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc"),
		storages.Description("a basic object storage for storing user information"),
		storages.Tags([]string{"tag1", "tag2"}),
		storages.Match("users"),
		storages.Regex(false),
		storages.Public(true),
		storages.Object(true, "15GB"),
	)
	if err != nil {
		return
	}

	// Display the Description
	fmt.Println(stg.Get().Description())

	// Open the config.yaml of the storage
	config, err := afero.ReadFile(fs, "/storages/test_stg.yaml")
	if err != nil {
		return
	}

	// Print config.yaml
	fmt.Println(string(config))

	// Output: a basic object storage for storing user information
	// id: QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc
	// description: a basic object storage for storing user information
	// tags:
	//     - tag1
	//     - tag2
	// match: users
	// useRegex: false
	// access:
	//     network: all
	// object:
	//     versioning: true
	//     size: 15GB
}
