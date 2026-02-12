package internal

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestVars(t *testing.T) {
	assert.Equal(t, GitUser, "taubyte-test")
	assert.Equal(t, Branch, "main")
	assert.Equal(t, ProjectName, "testproject")
	assert.Equal(t, ConfigRepo.Name, "tb_testproject")
	assert.Equal(t, ConfigRepo.ID, 485473636)
	assert.Assert(t, ConfigRepo.URL != "")
	assert.Equal(t, CodeRepo.Name, "tb_code_testproject")
	assert.Equal(t, CodeRepo.ID, 485473661)
}

func TestGitToken_SkipsWhenUnset(t *testing.T) {
	// GitToken skips the test when TEST_GIT_TOKEN is not set; we just ensure it doesn't panic
	t.Run("no token", func(t *testing.T) {
		GitToken(t) // will t.SkipNow() if env unset
	})
}
