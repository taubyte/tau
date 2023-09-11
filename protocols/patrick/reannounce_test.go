package service

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	dreamland "github.com/taubyte/tau/libdream"
	commonTest "github.com/taubyte/tau/libdream/helpers"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/monkey"
	_ "github.com/taubyte/tau/clients/p2p/patrick"
	protocolsCommon "github.com/taubyte/tau/protocols/common"
)

func TestReAnnounce(t *testing.T) {
	t.Skip("Needs to be refactored properly into wait for job to fail then do a sleep")
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"patrick": {},
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"auth":    {Others: map[string]int{"secure": 1}},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					Patrick: &commonIface.ClientConfig{},
					Monkey:  &commonIface.ClientConfig{},
				}.Compat(),
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

	protocolsCommon.FakeSecret = true
	err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	patrick, err := simples.Patrick()
	assert.NilError(t, err)

	jobs, err := patrick.List()
	if err != nil {
		t.Error(err)
		return
	}

	job_byte, err := patrick.Get(jobs[0])
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

	// Wait for reannounce to update attempts to 1 and send back to /jobs
	time.Sleep(10 * time.Second)

	retry_job, err := patrick.Get(job_byte.Id)
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

	// Wait for reannounce to update attempts to 2 and send back to /jobs
	time.Sleep(10 * time.Second)

	retry_job, err = patrick.Get(retry_job.Id)
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

	retry_job, err = patrick.Get(retry_job.Id)
	if err != nil {
		t.Error(err)
		return
	}

	if retry_job.Attempt != 2 {
		t.Errorf("Attempt should of stayed at 2 got %d", retry_job.Attempt)
		return
	}
}
