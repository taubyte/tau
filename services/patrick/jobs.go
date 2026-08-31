package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/pkg/git/repokey"
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
				repush = time.Now().Unix()-assignment.Timestamp > int64(p.reAnnounceJobTime.Seconds())
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

// getProjectIDFromJob resolves the project a push belongs to.
//
// AUTH IS THE ONLY ANSWER. There used to be a fallback here: when auth had no
// project for the repository, this looked the repository up in the TNS
// `repositories/<provider>/<id>/…` index and took the project id out of the key
// BY POSITION. That index is written by the config compiler and is still keyed
// by the provider's numeric id, while auth is now keyed by the clone URL — so
// the fallback answers in a different grammar from the question, and a positional
// read of a variable-depth key returns whatever segment happens to sit at that
// index. A wrong project id here builds one project's code into another's, so
// it says it does not know rather than guessing.
func (srv *PatrickService) getProjectIDFromJob(job *patrick.Job) (string, error) {
	// URI is the clone URL; SSHURL is the older field the same value used to
	// arrive in, which Job.Unmarshal promotes. A job built in memory has not
	// been through that, so the same promotion happens here.
	uri := job.Meta.Repository.URI
	if uri == "" {
		uri = job.Meta.Repository.SSHURL
	}

	key, err := repokey.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("job repository `%s`: %w", uri, err)
	}

	repo, err := srv.authClient.Repositories().Github().Get(key.String())
	if err != nil {
		return "", fmt.Errorf("repository `%s` is not registered with auth: %w", key, err)
	}

	projectID := repo.Project()
	if len(projectID) == 0 {
		return "", fmt.Errorf("repository `%s` is registered but linked to no project", key)
	}

	return projectID, nil
}
