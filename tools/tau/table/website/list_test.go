package websiteTable_test

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteTable "github.com/taubyte/tau/tools/tau/table/website"
)

func ExampleList() {
	websites := []*structureSpec.Website{
		{
			Id:       "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
			Name:     "someWebsite1",
			Provider: "github",
			RepoName: "taubyte-test/test_site1",
		},
		{
			Id:       "QmbUIDhRosp5BaXDASEWSCtpkQCgQCPdRVhnxjiSHfXdC0",
			Name:     "someWebsite2",
			Provider: "github",
			RepoName: "taubyte-test/test_site2",
		},
	}

	websiteTable.List(websites)

	// Output:
	// ┌─────────────────┬────────────────────────────────────────────┐
	// │ ID              │ NAME                                       │
	// │                 │ REPOSITORY                                 │
	// ├─────────────────┼────────────────────────────────────────────┤
	// │ QmbAA8...HfXdWH │ someWebsite1                               │
	// │                 │ https://github.com/taubyte-test/test_site1 │
	// ├─────────────────┼────────────────────────────────────────────┤
	// │ QmbUID...HfXdC0 │ someWebsite2                               │
	// │                 │ https://github.com/taubyte-test/test_site2 │
	// └─────────────────┴────────────────────────────────────────────┘
}
