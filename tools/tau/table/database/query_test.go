package databaseTable_test

import (
	"github.com/alecthomas/units"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	databaseTable "github.com/taubyte/tau/tools/tau/table/database"
)

func ExampleQuery_key() {
	database := &structureSpec.Database{
		Id:          "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name:        "someProject",
		Description: "this is a database of some type",
		Tags:        []string{"apple", "orange", "banana"},
		Match:       "/test/v1",
		Regex:       false,
		Local:       false,
		Key:         "someKey",
		Min:         15,
		Max:         30,
		Size:        uint64(units.MB),
	}

	databaseTable.Query(database)

	// Output:
	// ┌─────────────┬────────────────────────────────────────────────┐
	// │ ID          │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Name        │ someProject                                    │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Description │ this is a database of some type                │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Tags        │ apple, orange, banana                          │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Encryption  │ true                                           │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Access      │                                                │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │  -  Network │ all                                            │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Replicas    │                                                │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │  -  Min     │ 15                                             │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │  -  Max     │ 30                                             │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Storage     │                                                │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │  -  Size    │ 1MB                                            │
	// └─────────────┴────────────────────────────────────────────────┘
}

func ExampleQuery_no_key() {
	database := &structureSpec.Database{
		Id:          "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name:        "someProject",
		Description: "this is a database of some type",
		Tags:        []string{"apple", "orange", "banana"},
		Match:       "/test/v1",
		Regex:       false,
		Local:       false,
		Min:         15,
		Max:         30,
		Size:        uint64(units.MB),
	}

	databaseTable.Query(database)

	// Output:
	// ┌─────────────┬────────────────────────────────────────────────┐
	// │ ID          │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Name        │ someProject                                    │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Description │ this is a database of some type                │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Tags        │ apple, orange, banana                          │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Encryption  │ false                                          │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Access      │                                                │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │  -  Network │ all                                            │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Replicas    │                                                │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │  -  Min     │ 15                                             │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │  -  Max     │ 30                                             │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Storage     │                                                │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │  -  Size    │ 1MB                                            │
	// └─────────────┴────────────────────────────────────────────────┘
}
