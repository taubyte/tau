package jobs

import (
	"io/ioutil"
	"testing"

	commonTest "bitbucket.org/taubyte/dreamland-test/common"
	"gotest.tools/assert"

	_ "bitbucket.org/taubyte/hoarder/service"
	_ "bitbucket.org/taubyte/tns/service"
)

func TestRunWebsiteBasic(t *testing.T) {
	u, err := startDreamland("testRunWebsite")
	defer u.Stop()
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	logFile, err := ioutil.TempFile("", "config_log.txt")
	assert.NilError(t, err)

	job := newJob(commonTest.ConfigRepo, "job_for_config")

	jobContext := newTestContext(u.Context(), simple, logFile)
	err = jobContext.config(job)()
	assert.NilError(t, err)

	logFile, err = ioutil.TempFile("", "website_log.text")
	assert.NilError(t, err)

	job = newJob(commonTest.WebsiteRepo, "job_for_website")

	jobContext = newTestContext(u.Context(), simple, logFile)
	jobContext.ConfigRepoRoot = configRepoRoot
	err = jobContext.website(job)()
	assert.NilError(t, err)

	err = checkAsset(jobContext.Node, jobContext.Tns, "2a547229-190d-412b-b13a-a4fb5306dec9")
	assert.NilError(t, err)
}
