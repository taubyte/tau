package service

import (
	"testing"
	"time"

	commonTest "bitbucket.org/taubyte/dreamland-test/common"
	dreamlandCommon "bitbucket.org/taubyte/dreamland/common"
	dreamland "bitbucket.org/taubyte/dreamland/services"
	commonIface "github.com/taubyte/go-interfaces/common"

	_ "github.com/taubyte/odo/clients/p2p/monkey"
	_ "github.com/taubyte/odo/clients/p2p/patrick"
	"github.com/taubyte/odo/protocols/patrick/common"
)

func TestReAnnounce(t *testing.T) {
	t.Skip("Needs to be refactored properly into wait for job to fail then do a sleep")
	u := dreamland.Multiverse("testReannouce")
	defer u.Stop()

	err := u.StartWithConfig(&dreamlandCommon.Config{
		Services: map[string]commonIface.ServiceConfig{
			"patrick": {},
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"auth":    {Others: map[string]int{"secure": 1}},
		},
		Simples: map[string]dreamlandCommon.SimpleConfig{
			"client": {
				Clients: dreamlandCommon.SimpleConfigClients{
					Patrick: &commonIface.ClientConfig{},
					Monkey:  &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	mockAuthURL, err := u.GetURLHttps(u.Auth().Node())
	if err != nil {
		t.Error(err)
		return
	}

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	if err != nil {
		t.Error(err)
		return
	}

	simples, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	err = commonTest.RegisterTestRepositories(u.Context(), mockAuthURL, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	common.FakeSecret = true
	err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	jobs, err := simples.Patrick().List()
	if err != nil {
		t.Error(err)
		return
	}

	job_byte, err := simples.Patrick().Get(jobs[0])
	if err != nil {
		t.Error(err)
		return
	}

	old_attempt := job_byte.Attempt

	err = u.Monkey().Patrick().Lock(job_byte.Id, 10)
	if err != nil {
		t.Error(err)
		return
	}

	err = u.Monkey().Patrick().Failed(job_byte.Id, job_byte.Logs, nil)
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(10 * time.Second) // Wait for reannounce to update attempts to 1 and send back to /jobs

	retry_job, err := simples.Patrick().Get(job_byte.Id)
	if err != nil {
		t.Error(err)
		return
	}

	if old_attempt == retry_job.Attempt {
		t.Error("Attempts did not get updated to 1")
		return
	}

	err = u.Monkey().Patrick().Failed(retry_job.Id, retry_job.Logs, nil)
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(10 * time.Second) // Wait for reannounce to update attempts to 2 and send back to /jobs

	retry_job, err = simples.Patrick().Get(retry_job.Id)
	if err != nil {
		t.Error(err)
		return
	}

	if retry_job.Attempt != 2 {
		t.Errorf("Attempt did not get updated to two, got %d", retry_job.Attempt)
	}

	err = u.Monkey().Patrick().Failed(retry_job.Id, retry_job.Logs, nil)
	if err == nil {
		t.Error("Job should be stuck in archive/jobs since its attempts is already 2")
		return
	}

	retry_job, err = simples.Patrick().Get(retry_job.Id)
	if err != nil {
		t.Error(err)
		return
	}

	if retry_job.Attempt != 2 {
		t.Errorf("Attempt should of stayed at 2 got %d", retry_job.Attempt)
		return
	}
}
