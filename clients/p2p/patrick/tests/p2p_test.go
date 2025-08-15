package tests

import (
	_ "embed"
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/patrick"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	"gotest.tools/v3/assert"

	"github.com/fxamacker/cbor/v2"
	iface "github.com/taubyte/tau/core/services/patrick"
	servicesCommon "github.com/taubyte/tau/services/common"
	service "github.com/taubyte/tau/services/patrick"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func TestClientWithUniverse(t *testing.T) {
	t.Skip("Using an old token/project")
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	patrickHttpPort := 4443
	authHttpPort := 4445

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"patrick": {Others: map[string]int{"http": patrickHttpPort}},
			"auth":    {Others: map[string]int{"http": authHttpPort, "secure": 1}},
			"hoarder": {},
			"tns":     {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Patrick: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	if err != nil {
		t.Error(err)
		return
	}

	mockAuthURL, err := u.GetURLHttps(u.Auth().Node())
	if err != nil {
		t.Error(err)
		return
	}

	err = commonTest.RegisterTestRepositories(u.Context(), mockAuthURL, commonTest.ConfigRepo, commonTest.CodeRepo)
	if err != nil {
		t.Error(err)
		return
	}

	servicesCommon.FakeSecret = true
	err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	srv := u.Patrick()
	db := srv.KV()

	patrickClient, err := simple.Patrick()
	assert.NilError(t, err)

	jobs, err := patrickClient.List()
	if err != nil {
		t.Error("Failed calling list after error: ", err)
		return
	}

	var job *iface.Job

	job, err = patrickClient.Get(jobs[0])
	if err != nil {
		t.Error(err)
		return
	}

	// Testing on /jobs/
	if jobs[0] != job.Id {
		t.Errorf("Should have matching job ids. %s != %s", jobs[0], job.Id)
		return
	}

	err = patrickClient.Lock(job.Id, 5)
	if err != nil {
		t.Error(err)
		return
	}

	islocked, err := patrickClient.IsLocked(job.Id)
	if err != nil {
		t.Error(err)
		return
	}

	if !islocked {
		t.Error("Job not locked")
		return
	}

	err = patrickClient.Unlock(job.Id)
	if err != nil {
		t.Error(err)
		return
	}

	var lock service.Lock
	data, err := db.Get(u.Context(), "/locked/jobs/"+job.Id)
	if err != nil {
		t.Error(err)
		return
	}

	err = cbor.Unmarshal(data, &lock)
	if err != nil {
		t.Error(err)
		return
	}

	if lock.Eta != 0 && lock.Timestamp != 0 {
		t.Errorf("Expected eta and timestamp to be 0 to unlock got %d %d", lock.Eta, lock.Timestamp)
		return
	}

	testLogs := make(map[string]string, 0)
	testAssets := make(map[string]string, 0)

	testLogs["logs1"] = "logFile1"
	testLogs["logs2"] = "logFile2"
	testLogs["logs3"] = "logFile3"

	testAssets["asset1"] = "assetCID1"
	testAssets["asset2"] = "assetCID2"
	testAssets["asset3"] = "assetCID3"

	// Testing with /archive/jobs/
	err = patrickClient.Failed(job.Id, testLogs, testAssets)
	if err != nil {
		t.Error(err)
		return
	}

	jobs, err = patrickClient.List()
	if err != nil {
		t.Error("Failed calling list after error: ", err)
		return
	}

	if jobs[0] != job.Id {
		t.Error("Should have matching job ids")
		return
	}

	testJob, err := patrickClient.Get(job.Id)
	if err != nil {
		t.Errorf("Failed getting job %s with %v", job.Id, err)
	}

	if len(testJob.AssetCid) != 3 && len(testJob.Logs) != 3 {
		t.Errorf("Did not get length 3 for asset and logs got Assets: %d, Logs: %d", len(testJob.Logs), len(testJob.AssetCid))
	}
}
