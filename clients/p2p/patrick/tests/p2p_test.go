//go:build dreaming

package tests

import (
	_ "embed"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/auth/dream"
	_ "github.com/taubyte/tau/clients/p2p/patrick/dream"
	iface "github.com/taubyte/tau/core/services/patrick"
	servicesCommon "github.com/taubyte/tau/services/common"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func TestClientWithUniverse_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"patrick": {},
			"auth":    {},
			"hoarder": {},
			"tns":     {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Patrick: &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	assert.NilError(t, err)

	err = commonTest.CreateTestProject(u)
	assert.NilError(t, err)

	servicesCommon.FakeSecret = true
	err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo)
	assert.NilError(t, err)

	patrickClient, err := simple.Patrick()
	assert.NilError(t, err)

	jobs, err := patrickClient.List()
	assert.NilError(t, err)
	assert.Assert(t, len(jobs) > 0, "No jobs found")

	var job *iface.Job

	job, err = patrickClient.Get(jobs[0])
	assert.NilError(t, err)

	assert.Equal(t, jobs[0], job.Id)

	// Dequeue a job (replaces Lock)
	dequeuedJob, err := patrickClient.Dequeue()
	assert.NilError(t, err)
	assert.Assert(t, dequeuedJob != nil, "Expected a job from Dequeue")
	assert.Equal(t, dequeuedJob.Id, job.Id)

	// Verify assignment
	isAssigned, err := patrickClient.IsAssigned(job.Id)
	assert.NilError(t, err)
	assert.Equal(t, isAssigned, true)

	testLogs := make(map[string]string, 0)
	testAssets := make(map[string]string, 0)

	testLogs["logs1"] = "logFile1"
	testLogs["logs2"] = "logFile2"
	testLogs["logs3"] = "logFile3"

	testAssets["asset1"] = "assetCID1"
	testAssets["asset2"] = "assetCID2"
	testAssets["asset3"] = "assetCID3"

	err = patrickClient.Failed(job.Id, testLogs, testAssets)
	assert.NilError(t, err)

	jobs, err = patrickClient.List()
	assert.NilError(t, err)

	assert.Equal(t, jobs[0], job.Id)

	testJob, err := patrickClient.Get(job.Id)
	assert.NilError(t, err)

	assert.Equal(t, len(testJob.AssetCid), 3)
	assert.Equal(t, len(testJob.Logs), 3)
}
