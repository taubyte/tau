package service

import (
	"context"
	"errors"
	"strings"

	http "github.com/taubyte/tau/pkg/http"
)

// A job's logs are stored by content id, and /logs/{cid} served any of them to
// any valid token: the id was the only thing between a caller and another
// project's build output. Each log now points back at its job, and reading it
// takes that job's access.

const logOwnerPrefix = "/logs/by-cid/"

var errLogNotFound = errors.New("log not found")

// recordLogOwner notes which job each of logs belongs to.
func (srv *PatrickService) recordLogOwner(ctx context.Context, jid string, logs map[string]string) error {
	for _, cid := range logs {
		if cid == "" {
			continue
		}
		if err := srv.db.Put(ctx, logOwnerPrefix+cid, []byte(jid)); err != nil {
			return err
		}
	}
	return nil
}

// logOwner is the job a log belongs to. Logs stored before owners were recorded
// are found by looking through finished jobs once, and recorded then.
func (srv *PatrickService) logOwner(ctx context.Context, cid string) (string, error) {
	if jid, err := srv.db.Get(ctx, logOwnerPrefix+cid); err == nil && len(jid) > 0 {
		return string(jid), nil
	}

	keys, err := srv.db.List(ctx, "/archive/jobs/")
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		job, err := srv.getJob(ctx, "/archive/jobs/", strings.TrimPrefix(key, "/archive/jobs/"))
		if err != nil {
			continue
		}
		for _, logCid := range job.Logs {
			if logCid == cid {
				if err := srv.recordLogOwner(ctx, job.Id, job.Logs); err != nil {
					logger.Errorf("recording owners of job `%s` logs failed: %s", job.Id, err)
				}
				return job.Id, nil
			}
		}
	}
	return "", errLogNotFound
}

// withLogAccess serves a log to a caller with access to the job it belongs to.
// A log no job claims is not served.
func (srv *PatrickService) withLogAccess(handler func(http.Context) (interface{}, error)) func(http.Context) (interface{}, error) {
	return func(ctx http.Context) (interface{}, error) {
		cid, err := ctx.GetStringVariable("cid")
		if err != nil {
			return nil, err
		}
		jid, err := srv.logOwner(ctx.Request().Context(), cid)
		if err != nil {
			return nil, err
		}
		job, err := srv.anyJob(ctx.Request().Context(), jid)
		if err != nil {
			return nil, errLogNotFound
		}
		allowed, err := callerRepoAccess(ctx, job.Meta.Repository.ID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, errNoJobAccess
		}
		return handler(ctx)
	}
}
