package smartops_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestPretty(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	smart, err := project.SmartOps("test_smartops1", "")
	assert.NilError(t, err)

	assert.DeepEqual(t, smart.Prettify(nil), map[string]interface{}{
		"Id":          "smartops1ID",
		"Name":        "test_smartops1",
		"Description": "verifies node has GPU",
		"Tags":        []string{"smart_tag_1", "smart_tag_2"},
		"Source":      ".",
		"Timeout":     "6m40s",
		"Memory":      "16MB",
		"Call":        "ping1",
	})
}
