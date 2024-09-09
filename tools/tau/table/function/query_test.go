package functionTable_test

import (
	"time"

	"github.com/alecthomas/units"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/common"
	functionTable "github.com/taubyte/tau/tools/tau/table/function"
)

func getDefaultFunction() *structureSpec.Function {
	return &structureSpec.Function{
		Id:      "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name:    "someProject",
		Tags:    []string{"function_tag_1", "function_tag_2"},
		Timeout: uint64(20 * time.Second),
		Memory:  uint64(32 * units.GB),
		Call:    "ping",
		Source:  ".",
	}
}

func ExampleQuery_http() {
	function := getDefaultFunction()
	function.Description = "an http function for a simple ping"
	function.Type = common.FunctionTypeHttp
	function.Domains = []string{"test_domain1"}
	function.Method = "get"
	function.Paths = []string{"/ping"}

	functionTable.Query(function)

	// Output:
	// ┌─────────────┬────────────────────────────────────────────────┐
	// │ ID          │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Name        │ someProject                                    │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Description │ an http function for a simple ping             │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Tags        │ function_tag_1, function_tag_2                 │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Type        │ http                                           │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Timeout     │ 20s                                            │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Memory      │ 32GB                                           │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Method      │ get                                            │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Domains     │ test_domain1                                   │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Paths       │ /ping                                          │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Source      │ .                                              │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Call        │ ping                                           │
	// └─────────────┴────────────────────────────────────────────────┘
}

func ExampleQuery_p2p() {
	function := getDefaultFunction()
	function.Description = "a p2p function for a simple ping"
	function.Type = common.FunctionTypeP2P
	function.Protocol = "/test/v1"
	function.Command = "ping"
	function.Local = true

	functionTable.Query(function)

	// Output:
	// ┌─────────────┬────────────────────────────────────────────────┐
	// │ ID          │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Name        │ someProject                                    │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Description │ a p2p function for a simple ping               │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Tags        │ function_tag_1, function_tag_2                 │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Type        │ p2p                                            │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Timeout     │ 20s                                            │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Memory      │ 32GB                                           │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Protocol    │ /test/v1                                       │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Command     │ ping                                           │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Local       │ true                                           │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Source      │ .                                              │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Call        │ ping                                           │
	// └─────────────┴────────────────────────────────────────────────┘
}

func ExampleQuery_pubsub() {
	function := getDefaultFunction()
	function.Description = "a pubsub function for a simple ping"
	function.Type = common.FunctionTypePubSub
	function.Channel = "test_channel"

	functionTable.Query(function)

	// Output:
	// ┌─────────────┬────────────────────────────────────────────────┐
	// │ ID          │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Name        │ someProject                                    │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Description │ a pubsub function for a simple ping            │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Tags        │ function_tag_1, function_tag_2                 │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Type        │ pubsub                                         │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Timeout     │ 20s                                            │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Memory      │ 32GB                                           │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Channel     │ test_channel                                   │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Local       │ false                                          │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Source      │ .                                              │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Call        │ ping                                           │
	// └─────────────┴────────────────────────────────────────────────┘
}
