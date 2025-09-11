package service_test

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	spec "github.com/taubyte/tau/pkg/specs/common"
	servicesCommon "github.com/taubyte/tau/services/common"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/tns/dream"

	_ "github.com/taubyte/tau/clients/p2p/auth/dream"
	_ "github.com/taubyte/tau/clients/p2p/patrick/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
)

func TestDream(t *testing.T) {
	// Override default HTTP client to resolve test domains locally
	http.DefaultClient = commonTest.CreateHttpClient()

	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"patrick": {},
			"auth":    {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	auth, err := simple.Auth()
	assert.NilError(t, err)

	_patrick := u.Patrick()

	// Make sure no jobs are stored already
	db := _patrick.KV()
	jobs, err := db.List(u.Context(), "/jobs/")
	assert.NilError(t, err)
	assert.Equal(t, len(jobs), 0)

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	assert.NilError(t, err)

	err = commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.ConfigRepo)
	assert.NilError(t, err)

	servicesCommon.FakeSecret = true

	t.Run("Creation", func(t *testing.T) {
		err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo)
		assert.NilError(t, err)

		time.Sleep(1 * time.Second)

		jobs, err = db.List(u.Context(), "/jobs/")
		assert.NilError(t, err)
		assert.Equal(t, len(jobs), 1)

		job_byte, err := db.Get(u.Context(), jobs[0])
		assert.NilError(t, err)

		var job patrick.Job
		err = cbor.Unmarshal(job_byte, &job)
		assert.NilError(t, err)

		err = compareJobToPayload(job.Meta, commonTest.ConfigPayload)
		assert.NilError(t, err)
	})

	t.Run("ReportSsh", func(t *testing.T) {
		err = u.RunFixture("createProjectWithJobs")
		assert.NilError(t, err)

		// Check for 20 seconds after fixture is ran for the jobs
		attempts := 0
		var job *patrick.Job
		patrick, err := simple.Patrick()
		assert.NilError(t, err)

		for {
			attempts++
			assert.Assert(t, attempts < 20)

			jobs, err := patrick.List()
			assert.NilError(t, err)
			if len(jobs) < 2 {
				continue
			}

			job, err = patrick.Get(jobs[0])
			assert.NilError(t, err)
			if job != nil {
				break
			}

			time.Sleep(1 * time.Second)
		}

		tns, err := simple.TNS()
		assert.NilError(t, err)

		// TODO use go-spec
		resp, err := tns.Fetch(spec.NewTnsPath([]string{"resolve", "repo", "github", fmt.Sprintf("%d", job.Meta.Repository.ID), "ssh"}))
		if err != nil {
			t.Error(err)
			return
		}

		if resp.Interface() != job.Meta.Repository.SSHURL {
			t.Errorf("Response from tns does not match data from patrick, got `%v` != `%s`", resp, job.Meta.Repository.SSHURL)
			return
		}
	})

	t.Run("ReAnnounce", func(t *testing.T) {
		patrick, err := simple.Patrick()
		assert.NilError(t, err)

		jobs, err := patrick.List()
		assert.NilError(t, err)
		assert.Assert(t, len(jobs) > 0)

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
	})

}

func compareJobToPayload(meta patrick.Meta, payload []byte) (err error) {
	var _meta *patrick.Meta
	err = json.Unmarshal(payload, &_meta)
	if err != nil {
		return
	}

	type comparison struct {
		Before interface{}
		After  interface{}
		msg    string
	}

	compare := func(c *comparison) error {
		if !reflect.DeepEqual(c.Before, c.After) {
			return fmt.Errorf("%s doesn't match got %v expected %v", c.msg, c.Before, c.After)
		}

		return nil
	}

	comparisons := []*comparison{
		{Before: meta.Ref, After: _meta.Ref, msg: "Ref"},
		{Before: meta.Before, After: _meta.Before, msg: "Before"},
		{Before: meta.After, After: _meta.After, msg: "After"},
		{Before: meta.HeadCommit.ID, After: _meta.HeadCommit.ID, msg: "HeadCommit.ID"},
		{Before: meta.Repository.ID, After: _meta.Repository.ID, msg: "Repository.ID"},
		{Before: meta.Repository.SSHURL, After: _meta.Repository.SSHURL, msg: "Repository.SSHURL"},
	}

	for _, c := range comparisons {
		if err = compare(c); err != nil {
			return
		}
	}

	return
}
