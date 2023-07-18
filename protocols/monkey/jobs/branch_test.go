package jobs

import (
	"os"
	"testing"

	commonTest "bitbucket.org/taubyte/dreamland-test/common"
	protocolCommon "github.com/taubyte/odo/protocols/common"
	"gotest.tools/v3/assert"
)

func TestBranch(t *testing.T) {
	protocolCommon.LocalPatrick = true
	u, err := startDreamland("testRunBranch")
	defer u.Stop()
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	logFile, err := os.CreateTemp("/tmp", "library_test_log.txt")
	assert.NilError(t, err)

	job := newJob(commonTest.ConfigRepo, "job_for_config")
	jobContext := newTestContext(u.Context(), simple, logFile)
	job.Meta.Repository.Branch = "dreamland"

	err = jobContext.config(job)()
	assert.NilError(t, err)
}
