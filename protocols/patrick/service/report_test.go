package service

import (
	"fmt"
	"testing"
	"time"

	_ "bitbucket.org/taubyte/auth/service"
	commonDreamland "bitbucket.org/taubyte/dreamland/common"
	"bitbucket.org/taubyte/dreamland/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	spec "github.com/taubyte/go-specs/common"

	_ "bitbucket.org/taubyte/hoarder/service"
	_ "bitbucket.org/taubyte/monkey/service"
	_ "bitbucket.org/taubyte/seer/service"
	_ "bitbucket.org/taubyte/tns/service"
	"github.com/taubyte/go-interfaces/services/patrick"
	_ "github.com/taubyte/odo/protocols/patrick/api/p2p"
)

func TestReportSsh(t *testing.T) {
	u := services.Multiverse("ReportSsh")
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"patrick": {},
			"auth":    {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
				},
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
	for {
		attempts += 1

		jobs, err := simple.Patrick().List()
		if len(jobs) != 2 {
			err = fmt.Errorf("Expected 2 jobs got %d", len(jobs))
		}

		if err == nil {
			job, err = simple.Patrick().Get(jobs[0])
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

	// TODO use go-spec
	resp, err := simple.TNS().Fetch(spec.NewTnsPath([]string{"resolve", "repo", "github", fmt.Sprintf("%d", job.Meta.Repository.ID), "ssh"}))
	if err != nil {
		t.Error(err)
		return
	}

	if resp.Interface() != job.Meta.Repository.SSHURL {
		t.Errorf("Response from tns does not match data from patrick, got `%v` != `%s`", resp, job.Meta.Repository.SSHURL)
		return
	}
}
