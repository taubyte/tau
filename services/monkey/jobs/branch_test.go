//go:build dreaming

package jobs

import (
	"os"
	"testing"

	commonTest "github.com/taubyte/tau/dream/helpers"
	protocolCommon "github.com/taubyte/tau/services/common"
	"gotest.tools/v3/assert"
)

func TestBranch_Dreaming(t *testing.T) {
	t.Skip("Needs to be redone")
	protocolCommon.MockedPatrick = true

	u, cleanup, err := startDream(t)
	assert.NilError(t, err)
	defer cleanup()

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	logFile, err := os.CreateTemp("/tmp", "library_test_log.txt")
	assert.NilError(t, err)

	job := newJob(commonTest.ConfigRepo, "job_for_config")
	jobContext := newTestContext(u.Context(), simple, logFile)
	job.Meta.Repository.Branch = "dream"

	err = jobContext.config(job)()
	assert.NilError(t, err)
}
