package domains_test

import (
	"fmt"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/schema/domains"
	"github.com/taubyte/tau/pkg/schema/project"
)

func ExampleDomain() {
	// Create a new file system in memory
	fs := afero.NewMemMapFs()

	// Open a new project with a virtual file system
	project, err := project.Open(project.VirtualFS(fs, "/"))
	if err != nil {
		return
	}

	// Create or open an domain
	dom, err := project.Domain("test_dom", "")
	if err != nil {
		return
	}

	// Set and write domain fields
	err = dom.Set(true,
		domains.Id("QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc"),
		domains.Description("a basic domain"),
		domains.Tags([]string{"tag1", "tag2"}),
		domains.FQDN("hal.computers.com"),
	)
	if err != nil {
		return
	}

	// Display the Description
	fmt.Println(dom.Get().Description())

	// Open the config.yaml of the domain
	config, err := afero.ReadFile(fs, "/domains/test_dom.yaml")
	if err != nil {
		return
	}

	// Print config.yaml
	fmt.Println(string(config))

	// Output: a basic domain
	// id: QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc
	// description: a basic domain
	// tags:
	//     - tag1
	//     - tag2
	// fqdn: hal.computers.com
}
