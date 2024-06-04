package service

import (
	"fmt"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	spec "github.com/taubyte/tau/pkg/specs/common"
	_ "github.com/taubyte/tau/services/auth"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/patrick"
	"github.com/taubyte/tau/core/services/patrick"
	_ "github.com/taubyte/tau/services/hoarder"
	_ "github.com/taubyte/tau/services/monkey"
	_ "github.com/taubyte/tau/services/seer"
	_ "github.com/taubyte/tau/services/tns"
)

func TestReportSsh(t *testing.T) {
	t.Skip("Using an old token/project")
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
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("createProjectWithJobs")
	if err != nil {
		t.Errorf("Error with running fixture createProjectWithJobs: %v", err)
		return
	}

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	// Check for 20 seconds after fixture is ran for the jobs
	attempts := 0
	var job *patrick.Job
	patrick, err := simple.Patrick()
	assert.NilError(t, err)

	for {
		attempts += 1

		jobs, err := patrick.List()
		if len(jobs) != 2 {
			err = fmt.Errorf("Expected 2 jobs got %d", len(jobs))
		}

		if err == nil {
			job, err = patrick.Get(jobs[0])
			if err != nil {
				t.Error(err)
				return
			}
			break
		}

		if attempts == 20 {
			t.Error(err)
			return
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
