package smartops_test

import (
	"fmt"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/pkg/schema/smartops"
)

func ExampleSmartOps() {
	// Create a new file system in memory
	fs := afero.NewMemMapFs()

	// Open a new project with a virtual file system
	project, err := project.Open(project.VirtualFS(fs, "/"))
	if err != nil {
		return
	}

	// Create or open an smartop
	smart, err := project.SmartOps("test_smart", "")
	if err != nil {
		return
	}

	// Set and write smartop fields
	err = smart.Set(true,
		smartops.Id("QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc"),
		smartops.Description("a basic smartop"),
		smartops.Tags([]string{"tag1", "tag2"}),
		smartops.Source("."),
		smartops.Timeout("400s"),
		smartops.Memory("16MB"),
		smartops.Call("ping"),
	)
	if err != nil {
		return
	}

	// Display the Description
	fmt.Println(smart.Get().Description())

	// Open the config.yaml of the smartop
	config, err := afero.ReadFile(fs, "/smartops/test_smart.yaml")
	if err != nil {
		return
	}

	// Print config.yaml
	fmt.Println(string(config))

	// Output: a basic smartop
	// id: QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc
	// description: a basic smartop
	// tags:
	//     - tag1
	//     - tag2
	// source: .
	// execution:
	//     timeout: 400s
	//     memory: 16MB
	//     call: ping
}
