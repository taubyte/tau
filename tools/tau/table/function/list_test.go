package functionTable_test

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	functionTable "github.com/taubyte/tau/tools/tau/table/function"
)

func ExampleList() {
	functions := []*structureSpec.Function{
		{
			Id:   "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
			Name: "someFunction1",
			Call: "ping",
		},
		{
			Id:   "QmbUIDhRosp5BaXDASEWSCtpkQCgQCPdRVhnxjiSHfXdC0",
			Name: "someFunction2",
			Call: "testlib.ping",
		},
	}

	functionTable.List(functions)

	// Output:
	// ┌─────────────────┬───────────────┬──────────────┐
	// │ ID              │ NAME          │ CALL         │
	// ├─────────────────┼───────────────┼──────────────┤
	// │ QmbAA8...HfXdWH │ someFunction1 │ ping         │
	// ├─────────────────┼───────────────┼──────────────┤
	// │ QmbUID...HfXdC0 │ someFunction2 │ testlib.ping │
	// └─────────────────┴───────────────┴──────────────┘
}
