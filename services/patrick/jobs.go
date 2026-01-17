package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/services/patrick"
	ifaceTNS "github.com/taubyte/tau/core/services/tns"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"
)

// Job reannouncement functionality
func (p *PatrickService) ReannounceJobs(ctx context.Context) error {
	//Grab all job id's that are still in the list
	jobs, err := p.db.List(ctx, "/jobs/")
	if err != nil {
		return fmt.Errorf("failed grabbing all jobs with error: %v", err)
	}

	republished := 0
	for _, key := range jobs {
		// Doing this to only get the jid since id is /jobs/jid
		// Check if its currently locked and if it is we dont reannounce it
		skey := strings.Split(key, "/")
		if len(skey) < 3 {
			continue
		}

		jid := skey[2]
		republish := true

		if lockData, err := p.db.Get(ctx, "/locked/jobs/"+jid); err == nil {
			var jobLock Lock
			if err := cbor.Unmarshal(lockData, &jobLock); err == nil {
				republish = jobLock.Timestamp+jobLock.Eta < time.Now().Unix()
			}
		}

		if republish {
			if err = p.republishJob(ctx, jid); err != nil {
				return fmt.Errorf("failed republishing job %s with error: %w", jid, err)
			}
			republished++
			if republished >= MaxReAnnounceJobs {
				break
			}
		}
	}

	return nil
}

// Republish job helper
func (p *PatrickService) republishJob(ctx context.Context, jid string) error {
	job, err := p.db.Get(ctx, "/jobs/"+jid)
	if err != nil {
		return fmt.Errorf("get job %s failed with: %w", jid, err)
	}

	if err = p.node.PubSubPublish(ctx, patrickSpecs.PubSubIdent, job); err != nil {
		return fmt.Errorf("failed to send over in republishJob pubsub error: %w", err)
	}

	return nil
}

// Project operations
func (srv *PatrickService) connectToProject(ctx context.Context, job *patrick.Job) error {
	projectID, err := srv.getProjectIDFromJob(job)
	if err != nil {
		return err
	}

	err = srv.db.Put(ctx, fmt.Sprintf("/by/project/%s/%s", projectID, job.Id), []byte{})
	if err != nil {
		return fmt.Errorf("failed putting job into project with error: %w", err)
	}

	return nil
}

func (srv *PatrickService) getProjectIDFromJob(job *patrick.Job) (projectID string, err error) {
	repo, _ := srv.authClient.Repositories().Github().Get(job.Meta.Repository.ID)

	if repo != nil {
		projectID = repo.Project()
	}

	if len(projectID) == 0 {
		repo := job.Meta.Repository
		queryKey := []string{"repositories", strings.ToLower(repo.Provider), fmt.Sprintf("%d", repo.ID)}

		var resp interface{}
		resp, err = srv.tnsClient.Lookup(ifaceTNS.Query{Prefix: queryKey, RegEx: false})
		if err != nil {
			return
		}

		respArr, ok := resp.([]string)
		if !ok || len(respArr) == 0 {
			err = fmt.Errorf("response from lookup not an array or is empty: `%v`", resp)
			return
		}

		for _, key := range respArr {
			repoInfo := strings.Split(key, "/")
			if len(repoInfo) <= 4 {
				continue
			}
			projectID = repoInfo[4]
			if len(projectID) != 0 {
				break
			}
		}
		if len(projectID) == 0 {
			err = fmt.Errorf("projectID not found in response from tns for repo %d, got response: %s", repo.ID, resp)
		}
	}

	return
}
