package databaseTable_test

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	databaseTable "github.com/taubyte/tau/tools/tau/table/database"
)

func ExampleList() {
	databases := []*structureSpec.Database{
		{
			Id:    "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
			Name:  "someDatabase1",
			Match: "/test/v1",
		},
		{
			Id:    "QmbUIDhRosp5BaXDASEWSCtpkQCgQCPdRVhnxjiSHfXdC0",
			Name:  "someDatabase2",
			Match: "/test/v2",
		},
	}

	databaseTable.List(databases)

	// Output:
	// ┌─────────────────┬───────────────┬──────────┐
	// │ ID              │ NAME          │ MATCH    │
	// ├─────────────────┼───────────────┼──────────┤
	// │ QmbAA8...HfXdWH │ someDatabase1 │ /test/v1 │
	// ├─────────────────┼───────────────┼──────────┤
	// │ QmbUID...HfXdC0 │ someDatabase2 │ /test/v2 │
	// └─────────────────┴───────────────┴──────────┘
}
