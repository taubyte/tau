package functions_test

import (
	"errors"
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/pretty"
	commonSpec "github.com/taubyte/tau/pkg/specs/common"
	"gotest.tools/v3/assert"
)

func TestPrettyBasic(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	fun, err := project.Function("test_function1", "")
	assert.NilError(t, err)

	assert.DeepEqual(t, fun.Prettify(nil), map[string]interface{}{
		"Id":          "function1ID",
		"Name":        "test_function1",
		"Description": "an http function for a simple ping",
		"Tags":        []string{"function_tag_1", "function_tag_2"},
		"Type":        "http",
		"Method":      "get",
		"Paths":       []string{"/ping1"},
		"Source":      ".",
		"Domains":     []string{"test_domain1"},
		"Timeout":     "20s",
		"Memory":      "32GB",
		"Call":        "ping1",
	})

	fun, err = project.Function("test_function2", "test_app1")
	assert.NilError(t, err)

	assert.DeepEqual(t, fun.Prettify(nil), map[string]interface{}{
		"Id":          "function2ID",
		"Name":        "test_function2",
		"Description": "a pubsub function on channel 2 with a call to a library",
		"Tags":        []string{"function_tag_3", "function_tag_4"},
		"Type":        "pubsub",
		"Local":       true,
		"Channel":     "channel2",
		"Source":      "library/test_library2",
		"Timeout":     "23s",
		"Memory":      "23MB",
		"Call":        "ping2",
	})

	fun, err = project.Function("test_function3", "test_app2")
	assert.NilError(t, err)

	assert.DeepEqual(t, fun.Prettify(nil), map[string]interface{}{
		"Id":          "function3ID",
		"Name":        "test_function3",
		"Description": "a p2p function for ping over peer-2-peer",
		"Tags":        []string{"function_tag_5", "function_tag_6"},
		"Type":        "p2p",
		"Local":       false,
		"Command":     "command3",
		"Source":      ".",
		"Timeout":     "1h15m",
		"Memory":      "64GB",
		"Call":        "ping3",
		"Protocol":    "",
	})
}

func TestPrettyError(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	fun, err := project.Function("test_function1", "")
	assert.NilError(t, err)

	// Test with empty project ID
	prettier := internal.NewMockPrettier()
	prettier.Set().Project(func() string { return "" })

	_map := fun.Prettify(prettier)
	assert.ErrorContains(t, _map["Error"].(error), "project Id is empty")

	// Test with failing fetch method
	prettier = internal.NewMockPrettier()
	prettier.Set().Fetch(func(path *commonSpec.TnsPath) (pretty.Object, error) { return nil, errors.New("test error") })

	_map = fun.Prettify(prettier)
	assert.ErrorContains(t, _map["Error"].(error), "test error")

	// Test with valid cid
	prettier = internal.NewMockPrettier()
	prettier.Set().AssetCID("test_cid")

	_map = fun.Prettify(prettier)
	assert.Equal(t, _map["Asset"].(string), "test_cid")
}
