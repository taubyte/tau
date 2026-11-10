package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/services/patrick"
	ifaceTNS "github.com/taubyte/tau/core/services/tns"
	servicesCommon "github.com/taubyte/tau/services/common"
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
		if since, err := p.db.Get(ctx, waitingProjectPrefix+jid); err == nil && !p.projectFound(ctx, jid, since) {
			continue
		}
		repush := true

		if assignData, err := p.db.Get(ctx, "/assigned/"+jid); err == nil {
			var assignment Assignment
			if err := cbor.Unmarshal(assignData, &assignment); err == nil {
				// The owning Monkey refreshes its claim; only an expired one is re-pushed
				repush = time.Now().Unix()-assignment.Timestamp > int64(servicesCommon.DefaultLockTime.Seconds())
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
	if err := p.enqueue(jid); err != nil {
		return fmt.Errorf("failed to re-push job in republishJob: %w", err)
	}
	return nil
}

// A new website's or library's repository belongs to a project only once the
// config job has published it, which can be after the repository's first push.
// That job waits under waitingProjectPrefix, holding when it started, and is
// queued once its project is known, or after projectWaitMax.
const waitingProjectPrefix = "/waiting-project/"

var (
	projectWaitMax    = 10 * time.Minute
	errProjectUnknown = errors.New("repository not in a known project yet")
)

// projectFound links a waiting job to its project once the project is known,
// and reports whether the job may now be queued.
func (p *PatrickService) projectFound(ctx context.Context, jid string, since []byte) bool {
	job, err := p.getJob(ctx, "/jobs/", jid)
	if err != nil {
		return false
	}
	projectID, err := p.getProjectIDFromJob(job)
	if err == nil {
		err = p.linkToProject(ctx, job.Id, projectID)
	} else {
		start, _ := strconv.ParseInt(string(since), 10, 64)
		if time.Since(time.Unix(start, 0)) < projectWaitMax {
			return false
		}
		logger.Warnf("job %s: no project has repository %d after %s, queuing it anyway", jid, job.Meta.Repository.ID, projectWaitMax)
		err = nil
	}
	return err == nil && p.db.Delete(ctx, waitingProjectPrefix+jid) == nil
}

// devProjectLookupWait is how long a development node waits for a pushed
// repository's project to become known. A website's or library's project is
// published by the config job, which a push of the whole project has queued
// moments earlier; rejecting the push lost that repository's first build.
var devProjectLookupWait = 15 * time.Second

func (srv *PatrickService) connectToProject(ctx context.Context, job *patrick.Job) error {
	projectID, err := srv.getProjectIDFromJob(job)
	for deadline := time.Now().Add(devProjectLookupWait); err != nil && srv.devMode && time.Now().Before(deadline); {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		projectID, err = srv.getProjectIDFromJob(job)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", errProjectUnknown, err)
	}
	return srv.linkToProject(ctx, job.Id, projectID)
}

func (srv *PatrickService) linkToProject(ctx context.Context, jid, projectID string) error {
	if err := srv.db.Put(ctx, fmt.Sprintf("/by/project/%s/%s", projectID, jid), []byte{}); err != nil {
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
