package service_test

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/monkey/dream"
	_ "github.com/taubyte/tau/clients/p2p/patrick/dream"
	servicesCommon "github.com/taubyte/tau/services/common"
)

func TestReAnnounce(t *testing.T) {
	//t.Skip("Needs to be refactored properly into wait for job to fail then do a sleep")
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"patrick": {},
			//"monkey":  {},
			//"hoarder": {},
			"tns":  {},
			"auth": {Others: map[string]int{"secure": 1}},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Auth:    &commonIface.ClientConfig{},
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

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	if err != nil {
		t.Error(err)
		return
	}

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	auth, err := simple.Auth()
	assert.NilError(t, err)

	err = commonTest.RegisterTestProject(u.Context(), auth)
	assert.NilError(t, err)

	err = commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.ConfigRepo)
	assert.NilError(t, err)

	servicesCommon.FakeSecret = true
	err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo)
	assert.NilError(t, err)

	patrick, err := simple.Patrick()
	assert.NilError(t, err)

	jobs, err := patrick.List()
	assert.NilError(t, err)

	job_byte, err := patrick.Get(jobs[0])
	assert.NilError(t, err)

	old_attempt := job_byte.Attempt

	patrickClient, err := simple.Patrick()
	assert.NilError(t, err)

	err = patrickClient.Lock(job_byte.Id, 10)
	assert.NilError(t, err)

	err = patrickClient.Failed(job_byte.Id, job_byte.Logs, nil)
	assert.NilError(t, err)

	// Wait for reannounce to update attempts to 1 and send back to /jobs
	time.Sleep(10 * time.Second)

	retry_job, err := patrick.Get(job_byte.Id)
	assert.NilError(t, err)

	assert.Assert(t, old_attempt != retry_job.Attempt)

	err = patrickClient.Failed(retry_job.Id, retry_job.Logs, nil)
	assert.NilError(t, err)

	// Wait for reannounce to update attempts to 2 and send back to /jobs
	time.Sleep(10 * time.Second)

	retry_job, err = patrick.Get(retry_job.Id)
	assert.NilError(t, err)

	assert.Assert(t, retry_job.Attempt > old_attempt)

	err = patrickClient.Failed(retry_job.Id, retry_job.Logs, nil)
	assert.NilError(t, err)

	_, err = patrick.Get(retry_job.Id)
	assert.NilError(t, err)
}
