package storageTable_test

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	storageTable "github.com/taubyte/tau/tools/tau/table/storage"
)

func ExampleList() {
	storages := []*structureSpec.Storage{
		{
			Id:    "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
			Name:  "someStorage1",
			Match: "/test/v1",
		},
		{
			Id:    "QmbUIDhRosp5BaXDASEWSCtpkQCgQCPdRVhnxjiSHfXdC0",
			Name:  "someStorage2",
			Match: "/test/v2",
		},
	}

	storageTable.List(storages)

	// Output:
	// ┌─────────────────┬──────────────┬──────────┐
	// │ ID              │ NAME         │ MATCH    │
	// ├─────────────────┼──────────────┼──────────┤
	// │ QmbAA8...HfXdWH │ someStorage1 │ /test/v1 │
	// ├─────────────────┼──────────────┼──────────┤
	// │ QmbUID...HfXdC0 │ someStorage2 │ /test/v2 │
	// └─────────────────┴──────────────┴──────────┘
}
