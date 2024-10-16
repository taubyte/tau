package project_test

import (
	"testing"

	"github.com/spf13/afero"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/project"
	"gotest.tools/v3/assert"
)

func TestOpenError(t *testing.T) {
	_, err := project.Open(project.VirtualFS(afero.NewMemMapFs(), "invalid"))
	assert.ErrorContains(t, err, "opening repository failed with open invalid: file does not exist")
}

func TestOpenSystemFS(t *testing.T) {
	project, err := internal.NewProjectSystemFS()
	assert.NilError(t, err)

	assert.Equal(t, project.Get().Id(), "projectID1")
}
