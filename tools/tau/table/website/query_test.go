package websiteTable_test

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteTable "github.com/taubyte/tau/tools/tau/table/website"
)

func ExampleQuery() {
	website := &structureSpec.Website{
		Id:          "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name:        "someProject",
		Description: "this is a website of some type",
		Tags:        []string{"apple", "orange", "banana"},
		Domains:     []string{"hal.computers.com"},
		Paths:       []string{"/"},
		Branch:      "master",
		Provider:    "github",
		RepoID:      "591991",
		RepoName:    "taubyte/example",
	}

	websiteTable.Query(website)

	// Output:
	// ┌──────────────┬────────────────────────────────────────────────┐
	// │ ID           │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │ Name         │ someProject                                    │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │ Description  │ this is a website of some type                 │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │ Tags         │ apple, orange, banana                          │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │ Paths        │ /                                              │
	// ├──────────────┼────────────────────────────────────────────────┤
	// │ Domains      │ hal.computers.com                              │
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
