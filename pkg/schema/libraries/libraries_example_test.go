package libraries_test

import (
	"fmt"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/schema/libraries"
	"github.com/taubyte/tau/pkg/schema/project"
)

func ExampleLibrary() {
	// Create a new file system in memory
	fs := afero.NewMemMapFs()

	// Open a new project with a virtual file system
	project, err := project.Open(project.VirtualFS(fs, "/"))
	if err != nil {
		return
	}

	// Create or open an library
	lib, err := project.Library("test_lib", "")
	if err != nil {
		return
	}

	// Set and write library fields
	err = lib.Set(true,
		libraries.Id("QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc"),
		libraries.Description("a basic library"),
		libraries.Tags([]string{"tag1", "tag2"}),
		libraries.Path("/src"),
		libraries.Branch("main"),
		libraries.Github("222222222", "taubyte-test/library2"),
	)
	if err != nil {
		return
	}

	// Display the Description
	fmt.Println(lib.Get().Description())

	// Open the config.yaml of the library
	config, err := afero.ReadFile(fs, "/libraries/test_lib.yaml")
	if err != nil {
		return
	}

	// Print config.yaml
	fmt.Println(string(config))

	// Output: a basic library
	// id: QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc
	// description: a basic library
	// tags:
	//     - tag1
	//     - tag2
	// source:
	//     path: /src
	//     branch: main
	//     github:
	//         id: "222222222"
	//         fullname: taubyte-test/library2
}
