package functions_test

import (
	"fmt"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/schema/functions"
	"github.com/taubyte/tau/pkg/schema/project"
)

func ExampleFunction() {
	// Create a new file system in memory
	fs := afero.NewMemMapFs()

	// Open a new project with a virtual file system
	project, err := project.Open(project.VirtualFS(fs, "/"))
	if err != nil {
		return
	}

	// Create or open an function
	fun, err := project.Function("test_func", "")
	if err != nil {
		return
	}

	// Set and write function fields
	err = fun.Set(true,
		functions.Id("QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc"),
		functions.Description("a basic function"),
		functions.Tags([]string{"tag1", "tag2"}),
		functions.Type("https"),
		functions.Timeout("15s"),
		functions.Memory("64MB"),
		functions.Call("ping"),
		functions.Source("."),
		functions.Method("GET"),
		functions.Paths([]string{"/"}),
		functions.Domains([]string{"test_domain_1"}),
	)
	if err != nil {
		return
	}

	// Display the Description
	fmt.Println(fun.Get().Description())

	// Open the config.yaml of the function
	config, err := afero.ReadFile(fs, "/functions/test_func.yaml")
	if err != nil {
		return
	}

	// Print config.yaml
	fmt.Println(string(config))

	// Output: a basic function
	// id: QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc
	// description: a basic function
	// tags:
	//     - tag1
	//     - tag2
	// trigger:
	//     type: https
	//     method: GET
	//     paths:
	//         - /
	// execution:
	//     timeout: 15s
	//     memory: 64MB
	//     call: ping
	// source: .
	// domains:
	//     - test_domain_1
}
