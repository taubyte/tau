package engine

import (
	"testing"

	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func TestNew(t *testing.T) {
	s := &schemaDef{}
	p, err := New(s, yaseer.VirtualFS(afero.NewMemMapFs(), "/"))
	assert.NilError(t, err)

	assert.Assert(t, p != nil)

	instance, ok := p.(*instance)
	assert.Assert(t, ok)
	assert.Equal(t, instance.schema, s)
}

func TestNew_ErrorPath(t *testing.T) {
	s := &schemaDef{}

	// Create invalid seer option that will cause an error
	// Using an invalid path that doesn't exist and can't be created
	_, err := New(s, yaseer.SystemFS("/nonexistent/path/that/does/not/exist/and/cannot/be/created"))

	assert.ErrorContains(t, err, "parser failed to created seer")
}
