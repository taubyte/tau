package engine

import (
	"testing"

	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

// Test for New function
func TestNew(t *testing.T) {
	s := &schemaDef{}                                            // Create a default schemaDef instance
	p, err := New(s, yaseer.VirtualFS(afero.NewMemMapFs(), "/")) // Use the New method to get a Parser
	assert.NilError(t, err)

	// Assert
	assert.Assert(t, p != nil) // Ensure it's not nil

	// Type assertion
	instance, ok := p.(*instance)
	assert.Assert(t, ok)
	assert.Equal(t, instance.schema, s) // Ensure the schema is correctly set in the parser instance
}

// Test for New function with error path
func TestNew_ErrorPath(t *testing.T) {
	s := &schemaDef{}

	// Create invalid seer option that will cause an error
	// Using an invalid path that doesn't exist and can't be created
	_, err := New(s, yaseer.SystemFS("/nonexistent/path/that/does/not/exist/and/cannot/be/created"))

	// Should return error
	assert.ErrorContains(t, err, "parser failed to created seer")
}
