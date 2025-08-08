package libraries_test

import (
	"fmt"
	"runtime"
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/libraries"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func eql(t *testing.T, a [][]any) {
	_, file, line, _ := runtime.Caller(2)
	for idx, pair := range a {
		switch pair[0].(type) {
		case []string:
			comp := cmp.DeepEqual(pair[0], pair[1])
			assert.Check(t, comp, fmt.Sprintf("item(%d): %s:%d", idx, file, line))
		default:
			assert.Equal(t, pair[0], pair[1], fmt.Sprintf("item(%d): %s:%d", idx, file, line))
		}
	}
}

func assertLibrary1(t *testing.T, getter libraries.Getter) {
	provider, id, fullname := getter.Git()
	eql(t, [][]any{
		{getter.Id(), "library1ID"},
		{getter.Name(), "test_library1"},
		{getter.Description(), "just a library"},
		{getter.Tags(), []string{"library_tag_1", "library_tag_2"}},
		{getter.Path(), "/"},
		{getter.Branch(), "main"},
		{provider, "github"},
		{id, "111111111"},
		{fullname, "taubyte-test/library1"},
		{getter.Application(), ""},
		{len(getter.SmartOps()), 0},
	})
}

func assertLibrary2(t *testing.T, getter libraries.Getter) {
	provider, id, fullname := getter.Git()
	eql(t, [][]any{
		{getter.Id(), "library2ID"},
		{getter.Name(), "test_library2"},
		{getter.Description(), "just another library"},
		{getter.Tags(), []string{"library_tag_3", "library_tag_4"}},
		{getter.Path(), "/src"},
		{getter.Branch(), "dream"},
		{provider, "github"},
		{id, "222222222"},
		{fullname, "taubyte-test/library2"},
		{getter.Application(), "test_app1"},
		{len(getter.SmartOps()), 0},
	})
}

func TestGet(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	lib, err := project.Library("test_library1", "")
	assert.NilError(t, err)

	assertLibrary1(t, lib.Get())

	lib, err = project.Library("test_library2", "test_app1")
	assert.NilError(t, err)

	assertLibrary2(t, lib.Get())
}
