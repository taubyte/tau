package smartopsTable_test

import (
	"time"

	"github.com/alecthomas/units"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	smartopsTable "github.com/taubyte/tau/tools/tau/table/smartops"
)

func ExampleQuery() {
	smartops := &structureSpec.SmartOp{
		Id:          "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name:        "someProject",
		Description: "a simple smartops",
		Tags:        []string{"smartops_tag_1", "smartops_tag_2"},
		Timeout:     uint64(20 * time.Second),
		Memory:      uint64(32 * units.GB),
		Call:        "ping",
		Source:      ".",
	}
	smartopsTable.Query(smartops)

	// Output:
	// ┌─────────────┬────────────────────────────────────────────────┐
	// │ ID          │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Name        │ someProject                                    │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Description │ a simple smartops                              │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Tags        │ smartops_tag_1, smartops_tag_2                 │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Timeout     │ 20s                                            │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Memory      │ 32GB                                           │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Source      │ .                                              │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Call        │ ping                                           │
	// └─────────────┴────────────────────────────────────────────────┘
}
