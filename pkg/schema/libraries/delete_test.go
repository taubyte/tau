package libraries_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/libraries"
	"gotest.tools/v3/assert"
)

func TestDeleteBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	lib, err := project.Library("test_library2", "test_app1")
	assert.NilError(t, err)

	assertLibrary2(t, lib.Get())

	err = lib.Delete()
	assert.NilError(t, err)

	provider, id, fullname := lib.Get().Git()
	internal.AssertEmpty(t,
		lib.Get().Id(),
		lib.Get().Name(),
		lib.Get().Description(),
		lib.Get().Tags(),
		lib.Get().Path(),
		lib.Get().Branch(),
		provider,
		id,
		fullname,
	)

	local, _ := project.Get().Libraries("test_app1")
	assert.Equal(t, len(local), 0)

	lib, err = project.Library("test_library2", "test_app1")
	assert.NilError(t, err)
	assert.Equal(t, lib.Get().Name(), "test_library2")

	provider, id, fullname = lib.Get().Git()
	internal.AssertEmpty(t,
		lib.Get().Id(),
		lib.Get().Description(),
		lib.Get().Tags(),
		lib.Get().Path(),
		lib.Get().Branch(),
		provider,
		id,
		fullname,
	)
}

func TestDeleteAttributes(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	lib, err := project.Library("test_library1", "")
	assert.NilError(t, err)

	assertLibrary1(t, lib.Get())

	err = lib.Delete("description")
	assert.NilError(t, err)

	assertion := func(_lib libraries.Library) {
		provider, id, fullname := _lib.Get().Git()
		eql(t, [][]any{
			{_lib.Get().Id(), "library1ID"},
			{_lib.Get().Name(), "test_library1"},
			{_lib.Get().Description(), ""},
			{_lib.Get().Tags(), []string{"library_tag_1", "library_tag_2"}},
			{provider, "github"},
			{id, "111111111"},
			{fullname, "taubyte-test/library1"},
			{_lib.Get().Application(), ""},
		})
	}
	assertion(lib)

	// Re-open
	lib, err = project.Library("test_library1", "")
	assert.NilError(t, err)
	assertion(lib)
}
