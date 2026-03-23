package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/services/patrick"
	ifaceTNS "github.com/taubyte/tau/core/services/tns"
)

// ReannounceJobs scans pending jobs and pushes forgotten or expired ones back onto the queue.
func (p *PatrickService) ReannounceJobs(ctx context.Context) error {
	jobs, err := p.db.List(ctx, "/jobs/")
	if err != nil {
		return fmt.Errorf("failed grabbing all jobs with error: %v", err)
	}

	for _, key := range jobs {
		skey := strings.Split(key, "/")
		if len(skey) < 3 {
			continue
		}

		jid := skey[2]
		repush := true

		if assignData, err := p.db.Get(ctx, "/assigned/"+jid); err == nil {
			var assignment Assignment
			if err := cbor.Unmarshal(assignData, &assignment); err == nil {
				// Still within the assignment window — don't re-push
				repush = time.Now().Unix()-assignment.Timestamp > int64(DefaultReAnnounceJobTime.Seconds())
			}
		}

		if repush {
			if err = p.republishJob(ctx, jid); err != nil {
				return fmt.Errorf("failed republishing job %s with error: %w", jid, err)
			}
		}
	}

	return nil
}

// republishJob pushes a job back onto the queue (idempotent by id).
func (p *PatrickService) republishJob(ctx context.Context, jid string) error {
	if err := p.jobQueue.Push(jid, nil, 5*time.Second); err != nil {
		return fmt.Errorf("failed to re-push job in republishJob: %w", err)
	}
	return nil
}

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
