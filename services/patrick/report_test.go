package service_test

import (
	"fmt"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	spec "github.com/taubyte/tau/pkg/specs/common"
	_ "github.com/taubyte/tau/services/auth"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/patrick/dream"
	"github.com/taubyte/tau/core/services/patrick"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func TestReportSsh(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: "ReportSsh"})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
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
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("createProjectWithJobs")
	assert.NilError(t, err)

	simple, err := u.Simple("client")
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
}
