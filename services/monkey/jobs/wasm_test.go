package jobs

import (
	"os"
	"testing"

	commonTest "github.com/taubyte/tau/dream/helpers"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/hoarder"

	_ "github.com/taubyte/tau/services/tns"
)

func TestRunWasmBasic(t *testing.T) {
	t.Skip("Needs to be redone")
	u, err := startDreamland("testRunWasm")
	defer u.Stop()
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	logFile, err := os.CreateTemp("/tmp", "wasm_config_log.txt")
	assert.NilError(t, err)

	job := newJob(commonTest.ConfigRepo, "job_for_config")

	jobContext := newTestContext(u.Context(), simple, logFile)
	err = jobContext.config(job)()
	assert.NilError(t, err)

	logFile, err = os.CreateTemp("/tmp", "wasm_code_log.text")
	assert.NilError(t, err)

	job = newJob(commonTest.CodeRepo, "job_for_code")

	jobContext = newTestContext(u.Context(), simple, logFile)
	jobContext.ConfigRepoRoot = configRepoRoot
	err = jobContext.code(job)()
	assert.NilError(t, err)

	resourceIds := []string{"3a1d6781-4a74-42c2-81e0-221f32041825", "QmQazSMmMztAFkECFpvNjGMJpaYH4CvTTt85GDj1yYgt4a", "QmXybsgxX726s8t1uDbcJax5EL6m8LKkRFSJmcriQrVtKw", "QmQazSMmMztAFkECFpvNjGMJpaYH4CvHTt85GDj1yYgt4a"}
	err = checkAssets(jobContext.Node, jobContext.Tns, resourceIds)
	assert.NilError(t, err)
}
