package libraryTable_test

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	libraryTable "github.com/taubyte/tau/tools/tau/table/library"
)

func ExampleQuery() {
	library := &structureSpec.Library{
		Id:          "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name:        "someProject",
		Description: "this is a library of some type",
		Tags:        []string{"apple", "orange", "banana"},
		Path:        "/",
		Branch:      "master",
		Provider:    "github",
		RepoID:      "591991",
		RepoName:    "taubyte/example",
	}

	libraryTable.Query(library)

	// Output:
	// ┌──────────────┬────────────────────────────────────────────────┐
	// │ ID           │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │ Name         │ someProject                                    │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │ Description  │ this is a library of some type                 │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │ Tags         │ apple, orange, banana                          │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │ Path         │ /                                              │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │ Repository   │ https://github.com/taubyte/example             │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │  -  Name     │ taubyte/example                                │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │  -  ID       │ 591991                                         │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │  -  Provider │ github                                         │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │  -  Branch   │ master                                         │
	// └──────────────┴────────────────────────────────────────────────┘
}
