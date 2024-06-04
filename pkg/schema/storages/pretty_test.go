package storages_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestPrettyStreaming(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	stg, err := project.Storage("test_storage1", "")
	assert.NilError(t, err)

	assert.DeepEqual(t, stg.Prettify(nil), map[string]interface{}{
		"Id":          "storage1ID",
		"Name":        "test_storage1",
		"Description": "a streaming storage",
		"Tags":        []string{"storage_tag_1", "storage_tag_2"},
		"Match":       "photos",
		"Regex":       true,
		"Type":        "streaming",
		"TTL":         "5m",
		"Size":        "30GB",
	})
}

func TestPrettyObject(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	stg, err := project.Storage("test_storage2", "test_app1")
	assert.NilError(t, err)

	assert.DeepEqual(t, stg.Prettify(nil), map[string]interface{}{
		"Id":          "storage2ID",
		"Name":        "test_storage2",
		"Description": "an object storage",
		"Tags":        []string{"storage_tag_3", "storage_tag_4"},
		"Match":       "users",
		"Regex":       false,
		"Type":        "object",
		"Versioning":  true,
		"Public":      true,
		"Size":        "50GB",
	})
}
