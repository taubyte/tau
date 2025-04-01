package jobs

import (
	"os"
	"testing"

	commonTest "github.com/taubyte/tau/dream/helpers"
	"gotest.tools/v3/assert"
)

func TestRunWebsiteBasic(t *testing.T) {
	t.Skip("Needs to be redone")
	u, err := startDreamland("testRunWebsite")
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

	logFile, err = os.CreateTemp("/tmp", "website_log.text")
	assert.NilError(t, err)

	job = newJob(commonTest.WebsiteRepo, "job_for_website")

	jobContext = newTestContext(u.Context(), simple, logFile)
	jobContext.ConfigRepoRoot = configRepoRoot
	err = jobContext.website(job)()
	assert.NilError(t, err)

	err = checkAsset(jobContext.Node, jobContext.Tns, "2a547229-190d-412b-b13a-a4fb5306dec9")
	assert.NilError(t, err)
}
