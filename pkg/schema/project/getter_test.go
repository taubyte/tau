package project_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/spf13/afero"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/project"
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

func TestGetBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	eql(t, [][]any{
		{project.Get().Id(), "projectID1"},
		{project.Get().Name(), "TrueTest"},
		{project.Get().Description(), "a simple test project"},
		{project.Get().Tags(), []string{"tag1", "tag2"}},
		{project.Get().Email(), "cto@taubyte.com"},
	})
}

func TestGetError(t *testing.T) {
	project, err := project.Open(project.VirtualFS(afero.NewMemMapFs(), "/"))
	assert.NilError(t, err)
	assert.DeepEqual(t, project.Get().Applications(), []string(nil))
}
