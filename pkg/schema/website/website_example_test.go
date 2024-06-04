package website_test

import (
	"fmt"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/pkg/schema/website"
)

func ExampleWebsite() {
	// Create a new file system in memory
	fs := afero.NewMemMapFs()

	// Open a new project with a virtual file system
	project, err := project.Open(project.VirtualFS(fs, "/"))
	if err != nil {
		return
	}

	// Create or open an website
	web, err := project.Website("test_web", "")
	if err != nil {
		return
	}

	// Set and write website fields
	err = web.Set(true,
		website.Id("QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc"),
		website.Description("a basic website"),
		website.Tags([]string{"tag1", "tag2"}),
		website.Domains([]string{"hal_domain"}),
		website.Paths([]string{"/"}),
		website.Branch("main"),
		website.Github("222222222", "taubyte-test/basic_website"),
	)
	if err != nil {
		return
	}

	// Display the Description
	fmt.Println(web.Get().Description())

	// Open the config.yaml of the website
	config, err := afero.ReadFile(fs, "/websites/test_web.yaml")
	if err != nil {
		return
	}

	// Print config.yaml
	fmt.Println(string(config))

	// Output: a basic website
	// id: QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc
	// description: a basic website
	// tags:
	//     - tag1
	//     - tag2
	// domains:
	//     - hal_domain
	// source:
	//     paths:
	//         - /
	//     branch: main
	//     github:
	//         id: "222222222"
	//         fullname: taubyte-test/basic_website
}
