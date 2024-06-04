package databases_test

import (
	"fmt"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/schema/databases"
	"github.com/taubyte/tau/pkg/schema/project"
)

func ExampleDatabase() {
	// Create a new file system in memory
	fs := afero.NewMemMapFs()

	// Open a new project with a virtual file system
	project, err := project.Open(project.VirtualFS(fs, "/"))
	if err != nil {
		return
	}

	// Create or open an database
	db, err := project.Database("test_db", "")
	if err != nil {
		return
	}

	// Set and write database fields
	err = db.Set(true,
		databases.Id("QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc"),
		databases.Description("a database for users"),
		databases.Tags([]string{"tag1", "tag2"}),
		databases.Match("/users"),
		databases.Regex(true),
		databases.Local(false),
		databases.Replicas(2, 6),
		databases.Storage("50Kb"),
	)
	if err != nil {
		return
	}

	// Display the Description
	fmt.Println(db.Get().Description())

	// Open the config.yaml of the database
	config, err := afero.ReadFile(fs, "/databases/test_db.yaml")
	if err != nil {
		return
	}

	// Print config.yaml
	fmt.Println(string(config))

	// Output: a database for users
	// id: QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc
	// description: a database for users
	// tags:
	//     - tag1
	//     - tag2
	// match: /users
	// useRegex: true
	// access:
	//     network: all
	// replicas:
	//     min: 2
	//     max: 6
	// storage:
	//     size: 50Kb
}
