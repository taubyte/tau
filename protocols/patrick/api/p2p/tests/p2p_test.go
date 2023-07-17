package tests

import (
	_ "embed"
	"testing"

	_ "bitbucket.org/taubyte/auth/service"
	commonTest "bitbucket.org/taubyte/dreamland-test/common"
	dreamlandCommon "bitbucket.org/taubyte/dreamland/common"
	dreamland "bitbucket.org/taubyte/dreamland/services"
	_ "bitbucket.org/taubyte/hoarder/service"
	commonIface "github.com/taubyte/go-interfaces/common"
	_ "github.com/taubyte/odo/protocols/patrick/api/p2p"

	_ "bitbucket.org/taubyte/tns/service"
	"github.com/fxamacker/cbor/v2"
	iface "github.com/taubyte/go-interfaces/services/patrick"
	commonPatrick "github.com/taubyte/odo/protocols/patrick/common"
	"github.com/taubyte/odo/protocols/patrick/service"
	_ "github.com/taubyte/odo/protocols/patrick/service"
)

func TestClientWithUniverse(t *testing.T) {
	// dreamland.BigBang()
	u := dreamland.Multiverse("single")
	defer u.Stop()

	patrickHttpPort := 4443
	authHttpPort := 4445

	err := u.StartWithConfig(&dreamlandCommon.Config{
		Services: map[string]commonIface.ServiceConfig{
			"patrick": {Others: map[string]int{"http": patrickHttpPort}},
			"auth":    {Others: map[string]int{"http": authHttpPort, "secure": 1}},
			"hoarder": {},
			"tns":     {},
		},
		Simples: map[string]dreamlandCommon.SimpleConfig{
			"client": {
				Clients: dreamlandCommon.SimpleConfigClients{
					Patrick: &commonIface.ClientConfig{},
				},
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

	commonPatrick.FakeSecret = true
	err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	srv := u.Patrick()
	db := srv.KV()
	jobs, err := simple.Patrick().List()
	if err != nil {
		t.Error("Failed calling list after error: ", err)
		return
	}

	var job *iface.Job

	job, err = simple.Patrick().Get(jobs[0])
	if err != nil {
		t.Error(err)
		return
	}

	// Testing on /jobs/
	if jobs[0] != job.Id {
		t.Errorf("Should have matching job ids. %s != %s", jobs[0], job.Id)
		return
	}

	err = simple.Patrick().Lock(job.Id, 5)
	if err != nil {
		t.Error(err)
		return
	}

	islocked, err := simple.Patrick().IsLocked(job.Id)
	if err != nil {
		t.Error(err)
		return
	}

	if islocked == false {
		t.Error("Job not locked")
		return
	}

	err = simple.Patrick().Unlock(job.Id)
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
	err = simple.Patrick().Failed(job.Id, testLogs, testAssets)
	if err != nil {
		t.Error(err)
		return
	}

	jobs, err = simple.Patrick().List()
	if err != nil {
		t.Error("Failed calling list after error: ", err)
		return
	}

	if jobs[0] != job.Id {
		t.Error("Should have matching job ids")
		return
	}

	testJob, err := simple.Patrick().Get(job.Id)
	if err != nil {
		t.Errorf("Failed getting job %s with %v", job.Id, err)
	}

	if len(testJob.AssetCid) != 3 && len(testJob.Logs) != 3 {
		t.Errorf("Did not get length 3 for asset and logs got Assets: %d, Logs: %d", len(testJob.Logs), len(testJob.AssetCid))
	}
}
