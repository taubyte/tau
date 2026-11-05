package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	goHttp "net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	commonIface "github.com/taubyte/tau/core/services/patrick"
	http "github.com/taubyte/tau/pkg/http"
	httpAuth "github.com/taubyte/tau/pkg/http/auth"

	"github.com/google/go-github/v71/github"
	authService "github.com/taubyte/tau/services/auth"
)

var (
	errNoJobAccess = errors.New("no access to this job's repository")

	// repoAccessTTL is how long an answer from GitHub is reused, and so how long
	// a removed collaborator keeps access. A CLI listing a project asks about
	// the same repository once per job.
	repoAccessTTL = time.Minute
	repoAccessMax = 4096

	// projectAccessRepos bounds the GitHub calls listing one project can cost.
	projectAccessRepos = 8
)

type repoAccessEntry struct {
	allowed bool
	expires time.Time
}

type repoAccessCache struct {
	lock    sync.Mutex
	entries map[[sha256.Size]byte]repoAccessEntry
}

var repoAccesses = &repoAccessCache{entries: make(map[[sha256.Size]byte]repoAccessEntry)}

// check asks once per token and repository. ask also says whether its answer is
// certain: a denial because GitHub failed is not kept, or one bad answer would
// lock the repository's owner out of its jobs until the entry expired.
func (c *repoAccessCache) check(token string, repoID int, ask func(repoID string) (allowed, certain bool)) bool {
	key := sha256.Sum256([]byte(token + "\x00" + strconv.Itoa(repoID)))
	now := time.Now()

	c.lock.Lock()
	entry, ok := c.entries[key]
	c.lock.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.allowed
	}

	allowed, certain := ask(strconv.Itoa(repoID))
	if !allowed && !certain {
		return false
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	if len(c.entries) >= repoAccessMax {
		clear(c.entries)
	}
	c.entries[key] = repoAccessEntry{allowed: allowed, expires: now.Add(repoAccessTTL)}
	return allowed
}

// callerRepoAccess is the rule Auth applies to projects: write access to the
// repository. Read is not enough, a public repository reads to everyone.
func callerRepoAccess(ctx http.Context, repoID int) (bool, error) {
	auth := httpAuth.GetAuthorization(ctx)
	if auth == nil {
		return false, errNoJobAccess
	}
	client, ok := ctx.Variables()["GithubClient"].(authService.GitHubClient)
	if !ok {
		return false, errNoJobAccess
	}
	return repoAccesses.check(auth.Token, repoID, func(id string) (bool, bool) {
		return repoWriteAccess(client, id)
	}), nil
}

// repoWriteAccess is Auth's rule (write, maintain or admin on the repository),
// and whether a "no" is GitHub's answer rather than GitHub failing to answer.
func repoWriteAccess(client authService.GitHubClient, repoID string) (allowed, certain bool) {
	if err := client.GetByID(repoID); err != nil {
		var response *github.ErrorResponse
		if errors.As(err, &response) && response.Response != nil {
			switch response.Response.StatusCode {
			case goHttp.StatusNotFound, goHttp.StatusForbidden, goHttp.StatusUnauthorized:
				return false, true
			}
		}
		return false, false
	}
	repo := client.Cur()
	if repo == nil {
		return false, false
	}
	perms := repo.GetPermissions()
	return perms["admin"] || perms["maintain"] || perms["push"], true
}

// anyJob finds a job wherever it is stored, finished or not.
func (srv *PatrickService) anyJob(ctx context.Context, jid string) (*commonIface.Job, error) {
	job, err := srv.getJob(ctx, "/archive/jobs/", jid)
	if err != nil {
		return srv.getJob(ctx, "/jobs/", jid)
	}
	return job, nil
}

func (srv *PatrickService) authorizeJob(ctx http.Context, jid string) error {
	job, err := srv.anyJob(ctx.Request().Context(), jid)
	if err != nil {
		// the handler reports the unknown job; there is nothing to protect
		return nil
	}
	allowed, err := callerRepoAccess(ctx, job.Meta.Repository.ID)
	if err != nil {
		return err
	}
	if !allowed {
		return errNoJobAccess
	}
	return nil
}

// withJobAccess refuses a caller without access to the repository of the job
// named by the route's variable.
func (srv *PatrickService) withJobAccess(variable string, handler func(http.Context) (interface{}, error)) func(http.Context) (interface{}, error) {
	return func(ctx http.Context) (interface{}, error) {
		jid, err := ctx.GetStringVariable(variable)
		if err != nil {
			return nil, err
		}
		if err = srv.authorizeJob(ctx, jid); err != nil {
			return nil, err
		}
		return handler(ctx)
	}
}

// withProjectAccess refuses a caller with no access to any repository that has
// built in the project. A project without jobs has nothing to protect.
func (srv *PatrickService) withProjectAccess(handler func(http.Context) (interface{}, error)) func(http.Context) (interface{}, error) {
	return func(ctx http.Context) (interface{}, error) {
		projectID, err := ctx.GetStringVariable("projectId")
		if err != nil {
			return nil, err
		}

		prefix := fmt.Sprintf("/by/project/%s/", projectID)
		keys, err := srv.db.List(ctx.Request().Context(), prefix)
		if err != nil {
			return nil, fmt.Errorf("get failed with: %w", err)
		}
		if len(keys) == 0 {
			return handler(ctx)
		}

		seen := make(map[int]bool)
		for _, key := range keys {
			job, err := srv.anyJob(ctx.Request().Context(), strings.TrimPrefix(key, prefix))
			if err != nil || seen[job.Meta.Repository.ID] {
				continue
			}
			seen[job.Meta.Repository.ID] = true

			allowed, err := callerRepoAccess(ctx, job.Meta.Repository.ID)
			if err != nil {
				return nil, err
			}
			if allowed {
				return handler(ctx)
			}
			if len(seen) >= projectAccessRepos {
				break
			}
		}
		return nil, errNoJobAccess
	}
}
