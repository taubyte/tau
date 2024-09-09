package messagingTable_test

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	messagingTable "github.com/taubyte/tau/tools/tau/table/messaging"
)

func ExampleQuery() {
	messaging := &structureSpec.Messaging{
		Id:          "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name:        "someProject",
		Description: "this is a messaging of some type",
		Tags:        []string{"apple", "orange", "banana"},
		Match:       "/test/v1",
		MQTT:        true,
	}

	messagingTable.Query(messaging)

	// Output:
	// ┌───────────────┬────────────────────────────────────────────────┐
	// │ ID            │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├───────────────┼────────────────────────────────────────────────┤
	// │ Name          │ someProject                                    │
	// ├───────────────┼────────────────────────────────────────────────┤
	// │ Description   │ this is a messaging of some type               │
	// ├───────────────┼────────────────────────────────────────────────┤
	// │ Tags          │ apple, orange, banana                          │
	// ├───────────────┼────────────────────────────────────────────────┤
	// │ Local         │ false                                          │
	// ├───────────────┼────────────────────────────────────────────────┤
	// │ Channel       │                                                │
	// ├───────────────┼────────────────────────────────────────────────┤
	// │  -  Match     │ /test/v1                                       │
	// ├───────────────┼────────────────────────────────────────────────┤
	// │  -  Use Regex │ false                                          │
	// ├───────────────┼────────────────────────────────────────────────┤
	// │ Bridges       │                                                │
	// ├───────────────┼────────────────────────────────────────────────┤
	// │  -  MQTT      │ true                                           │
	// ├───────────────┼────────────────────────────────────────────────┤
	// │  -  WebSocket │ false                                          │
	// └───────────────┴────────────────────────────────────────────────┘
}
