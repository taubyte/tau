package libraryTable_test

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	libraryTable "github.com/taubyte/tau/tools/tau/table/library"
)

func ExampleList() {
	libraries := []*structureSpec.Library{
		{
			Id:       "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
			Name:     "someLibrary1",
			Provider: "github",
			RepoName: "taubyte-test/test_site1",
		},
		{
			Id:       "QmbUIDhRosp5BaXDASEWSCtpkQCgQCPdRVhnxjiSHfXdC0",
			Name:     "someLibrary2",
			Provider: "github",
			RepoName: "taubyte-test/test_site2",
		},
	}

	libraryTable.List(libraries)

	// Output:
	// ┌─────────────────┬────────────────────────────────────────────┐
	// │ ID              │ NAME                                       │
	// │                 │ REPOSITORY                                 │
	// ├─────────────────┼────────────────────────────────────────────┤
	// │ QmbAA8...HfXdWH │ someLibrary1                               │
	// │                 │ https://github.com/taubyte-test/test_site1 │
	// ├─────────────────┼────────────────────────────────────────────┤
	// │ QmbUID...HfXdC0 │ someLibrary2                               │
	// │                 │ https://github.com/taubyte-test/test_site2 │
	// └─────────────────┴────────────────────────────────────────────┘
}
