package jobs

import (
	"os"
	"testing"

	commonTest "github.com/taubyte/tau/dream/helpers"
	"gotest.tools/v3/assert"
)

func TestRunLibraryBasic(t *testing.T) {
	t.Skip("Needs to be redone")
	u, err := startDreamland("testRunLibrary")
	defer u.Stop()
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	logFile, err := os.CreateTemp("/tmp", "config_log.txt")
	assert.NilError(t, err)

	job := newJob(commonTest.ConfigRepo, "job_for_config")

	jobContext := newTestContext(u.Context(), simple, logFile)
	err = jobContext.config(job)()
	assert.NilError(t, err)

	logFile, err = os.CreateTemp("/tmp", "library_log.text")
	assert.NilError(t, err)

	job = newJob(commonTest.LibraryRepo, "job_for_library")

	jobContext = newTestContext(u.Context(), simple, logFile)
	jobContext.ConfigRepoRoot = configRepoRoot
	err = jobContext.library(job)()
	assert.NilError(t, err)

	err = checkAsset(jobContext.Node, jobContext.Tns, "Qmedz7rR2DgTapzUfK9yMHw8p9iW8wo9UjHAi6KkPZxxEu")
	assert.NilError(t, err)
}
