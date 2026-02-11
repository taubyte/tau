package prompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/prompts"
	"gotest.tools/v3/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, prompts.Required, "Required")
}
