package libraries_test

import (
	"errors"
	"fmt"
	"testing"

	internal "github.com/taubyte/tau/legacy/schema/internal/test"
	"github.com/taubyte/tau/legacy/schema/pretty"
	commonSpec "github.com/taubyte/tau/legacy/specs/common"
	"github.com/taubyte/tau/legacy/specs/methods"
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

// assetAt answers exactly one address and nothing else, so a lookup that asks
// for a different one is a miss rather than a silent hit — which is what the
// path-blind mock could not tell apart.
type assetAt struct {
	path string
	cid  string
}

func (a assetAt) Interface() interface{} { return a.cid }

// TestPrettyAssetIsAddressedByResourceId pins which of a library's two ids
// addresses its build asset. Monkey stashes the build under the library's own
// resource id and the substrate resolves it back by the same id; reading it
// under the git repository id instead hashes to a key nothing ever wrote, and
// the project that published the library is told it published no asset.
func TestPrettyAssetIsAddressedByResourceId(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	lib, err := project.Library("test_library1", "")
	assert.NilError(t, err)

	provider, gitID, _ := lib.Get().Git()
	assert.Equal(t, provider, "github")
	assert.Assert(t, gitID != lib.Get().Id(), "fixture cannot tell the two ids apart")

	prettier := internal.NewMockPrettier()
	published := assetAt{cid: "library_cid"}
	for _, branch := range prettier.Branches() {
		path, err := methods.GetTNSAssetPath(prettier.Project(), lib.Get().Id(), branch)
		assert.NilError(t, err)
		published.path = path.String()
	}

	prettier.Set().Fetch(func(path *commonSpec.TnsPath) (pretty.Object, error) {
		if path.String() != published.path {
			return nil, fmt.Errorf("nothing published at `%s`", path)
		}
		return published, nil
	})

	_map := lib.Prettify(prettier)
	assert.Equal(t, _map["Asset"], published.cid)
	assert.Assert(t, _map["Error"] == nil, "asset lookup failed with: %v", _map["Error"])
	assert.Equal(t, _map["GitId"], gitID)
}
