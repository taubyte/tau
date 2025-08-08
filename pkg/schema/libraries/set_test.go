package libraries_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/libraries"
	"gotest.tools/v3/assert"
)

func TestSetBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	lib, err := project.Library("test_library1", "")
	assert.NilError(t, err)

	assertLibrary1(t, lib.Get())

	var (
		id                 = "library3ID"
		description        = "this is test lib 3"
		tags               = []string{"lib_tag_5", "lib_tag_6"}
		path               = "/main"
		branch             = "dream"
		gitProvider        = "github"
		repositoryID       = "444444444"
		repositoryFullName = "taubyte_test/forty_four"
	)

	err = lib.Set(true,
		libraries.Id(id),
		libraries.Description(description),
		libraries.Tags(tags),
		libraries.Path(path),
		libraries.Branch(branch),
		libraries.Github(repositoryID, repositoryFullName),
	)
	assert.NilError(t, err)

	assertion := func(_lib libraries.Library) {
		provider, repoId, fullname := lib.Get().Git()
		eql(t, [][]any{
			{_lib.Get().Id(), id},
			{_lib.Get().Name(), "test_library1"},
			{_lib.Get().Description(), description},
			{_lib.Get().Tags(), tags},
			{_lib.Get().Path(), path},
			{_lib.Get().Branch(), branch},
			{provider, gitProvider},
			{repoId, repositoryID},
			{fullname, repositoryFullName},
			{_lib.Get().Application(), ""},
		})
	}
	assertion(lib)

	lib, err = project.Library("test_library1", "")
	assert.NilError(t, err)

	assertion(lib)
}

func TestSetInApp(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	lib, err := project.Library("test_library2", "test_app1")
	assert.NilError(t, err)

	assertLibrary2(t, lib.Get())

	var (
		id                 = "library3ID"
		description        = "this is test lib 3"
		tags               = []string{"lib_tag_5", "lib_tag_6"}
		path               = "/main"
		branch             = "dream"
		gitProvider        = "github"
		repositoryID       = "444444444"
		repositoryFullName = "taubyte_test/forty_four"
	)

	err = lib.Set(true,
		libraries.Id(id),
		libraries.Description(description),
		libraries.Tags(tags),
		libraries.Path(path),
		libraries.Branch(branch),
		libraries.Github(repositoryID, repositoryFullName),
		libraries.SmartOps([]string{"smart1"}),
	)
	assert.NilError(t, err)

	assertion := func(_lib libraries.Library) {
		provider, repoId, fullname := lib.Get().Git()
		eql(t, [][]any{
			{_lib.Get().Id(), id},
			{_lib.Get().Name(), "test_library2"},
			{_lib.Get().Description(), description},
			{_lib.Get().Tags(), tags},
			{_lib.Get().Path(), path},
			{_lib.Get().Branch(), branch},
			{provider, gitProvider},
			{repoId, repositoryID},
			{fullname, repositoryFullName},
			{_lib.Get().Application(), "test_app1"},
			{_lib.Get().SmartOps(), []string{"smart1"}},
		})
	}
	assertion(lib)

	lib, err = project.Library("test_library2", "test_app1")
	assert.NilError(t, err)

	assertion(lib)
}
