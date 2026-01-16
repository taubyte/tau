package engine

import (
	"testing"

	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func TestNew_ErrorPath(t *testing.T) {
	// Use case: Testing New with invalid seer options to trigger error path
	s := &schemaDef{}

	// Create invalid seer option that will cause an error
	// Using an invalid path that doesn't exist and can't be created
	_, err := New(s, yaseer.SystemFS("/nonexistent/path/that/does/not/exist/and/cannot/be/created"))

	// Should return error
	assert.ErrorContains(t, err, "parser failed to created seer")
}
