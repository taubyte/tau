package messagingTable_test

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	messagingTable "github.com/taubyte/tau/tools/tau/table/messaging"
)

func ExampleList() {
	channels := []*structureSpec.Messaging{
		{
			Id:    "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
			Name:  "someMessaging1",
			Match: "/test/v1",
		},
		{
			Id:    "QmbUIDhRosp5BaXDASEWSCtpkQCgQCPdRVhnxjiSHfXdC0",
			Name:  "someMessaging2",
			Match: "/test/v2",
		},
	}

	messagingTable.List(channels)

	// Output:
	// ┌─────────────────┬────────────────┬──────────┐
	// │ ID              │ NAME           │ MATCH    │
	// ├─────────────────┼────────────────┼──────────┤
	// │ QmbAA8...HfXdWH │ someMessaging1 │ /test/v1 │
	// ├─────────────────┼────────────────┼──────────┤
	// │ QmbUID...HfXdC0 │ someMessaging2 │ /test/v2 │
	// └─────────────────┴────────────────┴──────────┘
}
