package fixtures

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/helpers"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

// TODO: We have importProdProject and attachProdProject, and push Specific, this all needs to be cleaned up
func init() {
	dream.RegisterFixture("importProdProject", importProdProject)
}

type repo struct {
	branch          string
	repository_id   string
	repository_name string
}

func walkObject(r reflect.Value, repoCan chan repo) {
	if r.Kind() != reflect.Map {
		if r.Elem().Kind() != reflect.Map {
			return
		}
		r = r.Elem()
	}

	rid := r.MapIndex(reflect.ValueOf("repository-id"))
	rn := r.MapIndex(reflect.ValueOf("repository-name"))
	branch := r.MapIndex(reflect.ValueOf("branch"))

	if rid.IsValid() && rn.IsValid() && branch.IsValid() && rid.Elem().IsValid() && rn.Elem().IsValid() && branch.Elem().IsValid() {
		repoCan <- repo{
			branch:          branch.Elem().String(),
			repository_id:   rid.Elem().String(),
			repository_name: rn.Elem().String(),
		}
		return
	}

	var wg sync.WaitGroup
	for _, k := range r.MapKeys() {
		wg.Add(1)
		go func(k reflect.Value) {
			walkObject(r.MapIndex(k), repoCan)
			wg.Done()
		}(k)
	}
	wg.Wait()
}

func importProdProject(u *dream.Universe, params ...interface{}) error {
	if len(params) < 2 {
		return errors.New("importProdProject expects 2-3 parameters [project-id] [git-token] (branch)")
	}

	projectId := params[0].(string)
	if len(projectId) > 0 {
		helpers.ProjectID = projectId
	}
	gitToken := params[1].(string)
	if len(gitToken) > 0 {
		helpers.GitToken = gitToken
	}

	// Tracking how many jobs we run so that we can confirm we are waiting
	// for the right number of jobs to run
	var numJobs int

	err := attachProdProject(u, projectId, gitToken)
	if err != nil {
		return err
	}

	if SharedRepositoryData == nil {
		return fmt.Errorf("attaching prod project failed somehow")
	}

	simple, err := u.Simple("client")
	if err != nil {
		return err
	}

	time.Sleep(1 * time.Second)

	numJobs++
	err = u.RunFixture("pushSpecific", SharedRepositoryData.Configuration.Id, SharedRepositoryData.Configuration.Fullname, projectId, spec.DefaultBranches)
	if err != nil {
		return err
	}

	tnsClient, err := simple.TNS()
	if err != nil {
		return err
	}

	ctx, ctxC := context.WithTimeout(u.Context(), 30*time.Second)
	defer ctxC()
waitingForProject:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			for _, branch := range spec.DefaultBranches {
				if _, err := tnsClient.Fetch(spec.Current(projectId, branch)); err != nil {
					break waitingForProject
				}
			}
		}
	}

	numJobs++
	err = u.RunFixture("pushSpecific", SharedRepositoryData.Code.Id, SharedRepositoryData.Code.Fullname, projectId, spec.DefaultBranches)
	if err != nil {
		return err
	}

	patrickClient, err := simple.Patrick()
	if err != nil {
		return err
	}

	jobs, err := patrickClient.List()
	if err != nil {
		return err
	}

	// Wait for config job to be done within 15 seconds
	maxAttempts := 15
	var attempts int
	for attempts < maxAttempts {
		configJob := jobs[0]
		job, err := patrickClient.Get(configJob)
		if err != nil {
			return err
		}

		time.Sleep(1 * time.Second)
		if job.Status == patrick.JobStatusSuccess {
			break
		}

		attempts++
	}

	project, err := tnsClient.Simple().Project(projectId, spec.DefaultBranches...)
	if err != nil {
		return err
	}

	rProject := reflect.ValueOf(project)

	repoCan := make(chan repo, 64)

	go func() {
		walkObject(rProject, repoCan)
		close(repoCan)
	}()

	for r := range repoCan {
		numJobs++
		err = u.RunFixture("pushSpecific", r.repository_id, r.repository_name, projectId, spec.DefaultBranches)
		if err != nil {
			return err
		}
	}

	patrickJobs := make([]string, 0)

	// Wait for all jobs to be on patrick
	maxAttempts = 30
	attempts = 0
	for attempts < maxAttempts {
		patrickJobs, _ = patrickClient.List()

		if len(patrickJobs) == numJobs {
			break
		}

		time.Sleep(1 * time.Second)
		attempts++
	}

	if len(patrickJobs) != numJobs {
		return fmt.Errorf("all jobs didn't make it to patrick, got: %d expected %d", len(patrickJobs), numJobs)
	}

	// Wait for all jobs to finish
	maxAttempts = 300
	attempts = 0

	var failure bool
	for attempts < maxAttempts {
		failure = false
		for _, jid := range patrickJobs {
			job, _ := patrickClient.Get(jid)

			if job != nil {
				if job.Status != patrick.JobStatusSuccess {
					failure = true
				}
			}
		}

		if !failure {
			break
		}

		time.Sleep(1 * time.Second)
		attempts++
	}

	if failure {
		return errors.New("not all jobs succeeded after 5 minutes")
	}

	return nil
}
