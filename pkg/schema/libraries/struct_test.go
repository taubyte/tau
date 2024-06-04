package libraries_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"gotest.tools/v3/assert"
)

func TestStructBasic(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	lib, err := project.Library("test_library1", "")
	assert.NilError(t, err)

	err = lib.SetWithStruct(true, &structureSpec.Library{
		Id:          "library1ID",
		Description: "just a library",
		Tags:        []string{"library_tag_1", "library_tag_2"},
		Path:        "/",
		Branch:      "main",
		Provider:    "github",
		RepoID:      "111111111",
		RepoName:    "taubyte-test/library1",
	})
	assert.NilError(t, err)

	assertLibrary1(t, lib.Get())

	lib, err = project.Library("test_library2", "test_app1")
	assert.NilError(t, err)

	// Use different cert type
	err = lib.SetWithStruct(true, &structureSpec.Library{
		Id:          "library2ID",
		Description: "just another library",
		Tags:        []string{"library_tag_3", "library_tag_4"},
		Path:        "/src",
		Branch:      "dreamland",
		Provider:    "github",
		RepoID:      "222222222",
		RepoName:    "taubyte-test/library2",
	})
	assert.NilError(t, err)

	assertLibrary2(t, lib.Get())
}

func TestStructError(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	lib, err := project.Library("test_library1", "")
	assert.NilError(t, err)

	err = lib.SetWithStruct(true, &structureSpec.Library{
		Id:       "library1ID",
		SmartOps: []string{"smart1"},
	})
	assert.NilError(t, err)

	eql(t, [][]any{
		{lib.Get().Id(), "library1ID"},
		{lib.Get().SmartOps(), []string{"smart1"}},
	})

	err = lib.SetWithStruct(true, &structureSpec.Library{
		Provider: "unsupported",
	})
	assert.ErrorContains(t, err, "Git provider `unsupported` not supported")
}
