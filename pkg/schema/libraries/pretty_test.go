package libraries_test

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

	lib, err := project.Library("test_library1", "")
	assert.NilError(t, err)

	assert.DeepEqual(t, lib.Prettify(nil), map[string]interface{}{
		"Id":          "library1ID",
		"Description": "just a library",
		"Name":        "test_library1",
		"Tags":        []string{"library_tag_1", "library_tag_2"},
		"Path":        "/",
		"Branch":      "main",
		"GitFullName": "taubyte-test/library1",
		"GitId":       "111111111",
		"GitProvider": "github",
	})
}

func TestPrettyError(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	lib, err := project.Library("test_library1", "")
	assert.NilError(t, err)

	// Test with empty project ID
	prettier := internal.NewMockPrettier()
	prettier.Set().Project(func() string { return "" })

	_map := lib.Prettify(prettier)
	assert.ErrorContains(t, _map["Error"].(error), "project Id is empty")

	// Test with failing fetch method
	prettier = internal.NewMockPrettier()
	prettier.Set().Fetch(func(path *commonSpec.TnsPath) (pretty.Object, error) { return nil, errors.New("test error") })

	_map = lib.Prettify(prettier)
	assert.ErrorContains(t, _map["Error"].(error), "test error")

	// Test with valid cid
	prettier = internal.NewMockPrettier()
	prettier.Set().AssetCID("test_cid")

	_map = lib.Prettify(prettier)
	assert.Equal(t, _map["Asset"].(string), "test_cid")
}
