package functions_test

import (
	"testing"
	"time"

	"github.com/alecthomas/units"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"gotest.tools/v3/assert"
)

func TestStructHttp(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	fun, err := project.Function("test_function1", "")
	assert.NilError(t, err)

	err = fun.SetWithStruct(true, &structureSpec.Function{
		Id:          "function1ID",
		Name:        "test_function1",
		Description: "an http function for a simple ping",
		Tags:        []string{"function_tag_1", "function_tag_2"},
		Type:        "http",
		Timeout:     uint64(20 * time.Second),
		Memory:      uint64(32 * units.GB),
		Call:        "ping1",
		Source:      ".",
		Domains:     []string{"test_domain1"},
		Method:      "get",
		Paths:       []string{"/ping1"},
		SmartOps:    []string{},
	})
	assert.NilError(t, err)

	assertFunction1_http(t, fun.Get())
}

func TestStructPubSub(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	fun, err := project.Function("test_function2", "test_app1")
	assert.NilError(t, err)

	err = fun.SetWithStruct(true, &structureSpec.Function{
		Id:          "function2ID",
		Name:        "test_function2",
		Description: "a pubsub function on channel 2 with a call to a library",
		Tags:        []string{"function_tag_3", "function_tag_4"},
		Type:        "pubsub",
		Local:       true,
		Channel:     "channel2",
		Source:      "library/test_library2",
		Timeout:     uint64(23 * time.Second),
		Memory:      uint64(23 * units.MB),
		Call:        "ping2",
	})
	assert.NilError(t, err)

	assertFunction2_pubsub(t, fun.Get())
}

func TestStructP2P(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	fun, err := project.Function("test_function3", "test_app2")
	assert.NilError(t, err)

	err = fun.SetWithStruct(true, &structureSpec.Function{
		Id:          "function3ID",
		Name:        "test_function3",
		Description: "a p2p function for ping over peer-2-peer",
		Tags:        []string{"function_tag_5", "function_tag_6"},
		Type:        "p2p",
		Local:       false,
		Command:     "command3",
		Source:      ".",
		Timeout:     uint64(1*time.Hour + 15*time.Minute),
		Memory:      uint64(64 * units.GB),
		Call:        "ping3",
		Protocol:    "",
	})
	assert.NilError(t, err)

	assertFunction3_p2p(t, fun.Get())
}

func TestStructError(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	fun, err := project.Function("test_function1", "")
	assert.NilError(t, err)

	err = fun.SetWithStruct(true, nil)
	assert.ErrorContains(t, err, "nil pointer")
}
