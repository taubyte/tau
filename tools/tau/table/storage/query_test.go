package storageTable_test

import (
	"github.com/alecthomas/units"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	storageTable "github.com/taubyte/tau/tools/tau/table/storage"
)

func ExampleQuery_object() {
	storage := &structureSpec.Storage{
		Id:          "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name:        "someProject",
		Description: "this is a storage of some type",
		Tags:        []string{"apple", "orange", "banana"},
		Match:       "/test/v1",
		Regex:       false,
		Type:        "Object",
		Public:      false,
		Size:        uint64(39 * units.MB),
		Versioning:  false,
	}

	storageTable.Query(storage)

	// Output:
	// ┌────────────────┬────────────────────────────────────────────────┐
	// │ ID             │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├────────────────┼────────────────────────────────────────────────┤
	// │ Name           │ someProject                                    │
	// ├────────────────┼────────────────────────────────────────────────┤
	// │ Description    │ this is a storage of some type                 │
	// ├────────────────┼────────────────────────────────────────────────┤
	// │ Tags           │ apple, orange, banana                          │
	// ├────────────────┼────────────────────────────────────────────────┤
	// │ Access         │                                                │
	// ├────────────────┼────────────────────────────────────────────────┤
	// │  -  Network    │ host                                           │
	// ├────────────────┼────────────────────────────────────────────────┤
	// │ Object         │                                                │
	// ├────────────────┼────────────────────────────────────────────────┤
	// │  -  Versioning │ false                                          │
	// ├────────────────┼────────────────────────────────────────────────┤
	// │  -  Size       │ 39MB                                           │
	// └────────────────┴────────────────────────────────────────────────┘
}
func ExampleQuery_streaming() {
	storage := &structureSpec.Storage{
		Id:          "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name:        "someProject",
		Description: "this is a storage of some type",
		Tags:        []string{"apple", "orange", "banana"},
		Match:       "/test/v1",
		Regex:       false,
		Type:        "Streaming",
		Public:      true,
		Size:        uint64(4 * units.MB),
		Ttl:         50000,
	}

	storageTable.Query(storage)

	// Output:
	// ┌─────────────┬────────────────────────────────────────────────┐
	// │ ID          │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Name        │ someProject                                    │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Description │ this is a storage of some type                 │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Tags        │ apple, orange, banana                          │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Access      │                                                │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │  -  Network │ all                                            │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Streaming   │                                                │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │  -  TTL     │ 50µs                                           │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │  -  Size    │ 4MB                                            │
	// └─────────────┴────────────────────────────────────────────────┘
}
