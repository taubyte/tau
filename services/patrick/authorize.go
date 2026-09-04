package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	commonIface "github.com/taubyte/tau/core/services/patrick"
	http "github.com/taubyte/tau/pkg/http"
	authService "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/utils/maps"
)

// errJobNotFound is the answer for a job that does not exist and for one that
// exists but is not the caller's. Distinguishing them would turn every job
// route into an id oracle, which is the same reason auth words its own refusal
// this way.
var errJobNotFound = errors.New("job not found")

// errLogNotFound is the same refusal for a log cid: one that was never indexed
// and one belonging to somebody else's job read alike.
var errLogNotFound = errors.New("log not found")

// authorized is the auth block a route that serves something a project owns
// declares: authenticate the caller, then authorize the object the path names.
func (srv *PatrickService) authorized(object func(http.Context) error) http.RouteAuthHandler {
	return authService.RouteAuth(srv.GitHubTokenHTTPAuth, srv.GitHubTokenHTTPAuthCleanup, object)
}

// authorizeProjectVariable authorizes the project the route names directly.
func (srv *PatrickService) authorizeProjectVariable(ctx http.Context) error {
	if srv.devMode {
		return nil
	}

	projectId, err := maps.String(ctx.Variables(), "projectId")
	if err != nil {
		return err
	}

	return authService.AuthorizeProject(ctx, srv.authClient, projectId)
}

// authorizeJobVariable authorizes the project that owns the job the route
// names. A job id is an address, not a credential: knowing one says nothing
// about being allowed to read its logs or its build artifacts.
func (srv *PatrickService) authorizeJobVariable(variable string) func(http.Context) error {
	return func(ctx http.Context) error {
		if srv.devMode {
			return nil
		}

		jid, err := maps.String(ctx.Variables(), variable)
		if err != nil {
			return err
		}

		job, err := srv.anyJob(ctx.Request().Context(), jid)
		if err != nil {
			return errJobNotFound
		}

		projectID, err := srv.getProjectIDFromJob(job)
		if err != nil {
			return errJobNotFound
		}

		if err := authService.AuthorizeProject(ctx, srv.authClient, projectID); err != nil {
			return errJobNotFound
		}

		return nil
	}
}

// anyJob finds a job wherever it currently lives. A job moves from /jobs/ to
// /archive/jobs/ when it finishes, and authorization has to hold on both sides
// of that move.
func (srv *PatrickService) anyJob(ctx context.Context, jid string) (*commonIface.Job, error) {
	job, err := srv.getJob(ctx, "/jobs/", jid)
	if err == nil {
		return job, nil
	}

	return srv.getJob(ctx, "/archive/jobs/", jid)
}

// authorizeLogVariable authorizes the project that owns the job a log cid came
// from. A cid is an address, not a credential — the route hands back whatever
// content the node holds under it, so the only thing that can stand between one
// project's build logs and another project's owner is knowing whose job wrote
// it. That is what jobLogIndex records.
func (srv *PatrickService) authorizeLogVariable(variable string) func(http.Context) error {
	return func(ctx http.Context) error {
		if srv.devMode {
			return nil
		}

		cid, err := maps.String(ctx.Variables(), variable)
		if err != nil {
			return err
		}

		requestCtx := ctx.Request().Context()
		prefix := jobLogIndex(cid, "")
		keys, err := srv.db.List(requestCtx, prefix)
		if err != nil || len(keys) == 0 {
			return errLogNotFound
		}

		// One cid can be the log of more than one job: identical bytes hash to
		// one address. The caller needs a claim on any one of them.
		for _, key := range keys {
			job, err := srv.anyJob(requestCtx, strings.TrimPrefix(key, prefix))
			if err != nil {
				continue
			}

			projectID, err := srv.getProjectIDFromJob(job)
			if err != nil {
				continue
			}

			if authService.AuthorizeProject(ctx, srv.authClient, projectID) == nil {
				return nil
			}
		}

		return errLogNotFound
	}
}

// jobLogIndex is the key under which a job claims a log cid. The cid is a path
// segment of its own and the job id another, so two jobs whose logs hash alike
// each write their own key rather than overwriting one another's.
func jobLogIndex(cid, jid string) string {
	return "/by/log/" + cid + "/" + jid
}

// indexJobLogs records which jobs a log cid belongs to. Logs are addressed by
// content, and a content address carries no owner, so ownership has to be
// written down at the moment the job reports the log.
func (srv *PatrickService) indexJobLogs(ctx context.Context, job *commonIface.Job) error {
	for _, cid := range job.Logs {
		if cid == "" {
			continue
		}

		if err := srv.db.Put(ctx, jobLogIndex(cid, job.Id), []byte{}); err != nil {
			return fmt.Errorf("indexing log %s of job %s failed with: %w", cid, job.Id, err)
		}
	}

	return nil
}
