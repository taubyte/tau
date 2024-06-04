package website_test

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

	web, err := project.Website("test_website1", "")
	assert.NilError(t, err)

	assert.DeepEqual(t, web.Prettify(nil), map[string]interface{}{
		"Id":          "website1ID",
		"Description": "a simple photo booth",
		"Name":        "test_website1",
		"Tags":        []string{"website_tag_1", "website_tag_2"},
		"Branch":      "main",
		"GitFullName": "taubyte-test/photo_booth",
		"GitId":       "111111111",
		"GitProvider": "github",
		"Domains":     []string{"test_domain1"},
		"Paths":       []string{"/photos"},
	})
}

func TestPrettyError(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	web, err := project.Website("test_website1", "")
	assert.NilError(t, err)

	// Test with empty project ID
	prettier := internal.NewMockPrettier()
	prettier.Set().Project(func() string { return "" })

	_map := web.Prettify(prettier)
	assert.ErrorContains(t, _map["Error"].(error), "project Id is empty")

	// Test with failing fetch method
	prettier = internal.NewMockPrettier()
	prettier.Set().Fetch(func(path *commonSpec.TnsPath) (pretty.Object, error) { return nil, errors.New("test error") })

	_map = web.Prettify(prettier)
	assert.ErrorContains(t, _map["Error"].(error), "test error")

	// Test with valid cid
	prettier = internal.NewMockPrettier()
	prettier.Set().AssetCID("test_cid")

	_map = web.Prettify(prettier)
	assert.Equal(t, _map["Asset"].(string), "test_cid")
}
