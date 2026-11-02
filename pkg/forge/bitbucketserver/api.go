package bitbucketserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// pageLimit is what this driver asks a page to hold. Bitbucket may clamp it.
const pageLimit = 100

// maxPages bounds a listing a forge never ends.
const maxPages = 1000

// Bitbucket's repository permissions, as /repos filters by them.
const (
	permAdmin = "REPO_ADMIN"
	permWrite = "REPO_WRITE"
	permRead  = "REPO_READ"
)

// The slug comes from the whoami servlet, the only "who am I" on Bitbucket
// Server; the user record gives the numeric id, which is never reassigned.
func (c *client) Whoami(ctx context.Context) (forge.Identity, error) {
	var slug string
	if err := c.send(ctx, routeWhoami.at(), nil, &slug); err != nil {
		return forge.Identity{}, c.classify(err, repokey.Key{})
	}
	slug = strings.TrimSpace(slug)
	if slug == "" || len(slug) > maxHostString-1 {
		return forge.Identity{}, errors.New("forge/bitbucketserver: the forge answered no user, so this credential belongs to nobody")
	}
	type userJSON struct {
		Slug  string `json:"slug"`
		ID    int64  `json:"id"`
		Email string `json:"emailAddress"`
	}
	var u userJSON
	if err := c.send(ctx, routeUser.at(slug), nil, &u); err != nil {
		return forge.Identity{}, c.classify(err, repokey.Key{})
	}
	if u.Slug != slug || u.ID == 0 {
		return forge.Identity{}, fmt.Errorf("forge/bitbucketserver: %s was asked for the user %q and answered about another", c.d.host, bounded(slug))
	}
	return forge.Identity{Login: u.Slug, Subject: strconv.FormatInt(u.ID, 10), Email: u.Email}, nil
}

// The repository is fetched first, telling a missing or hidden one from a
// visible one, then the permission-filtered listings are asked whether it is
// among those this credential administers or writes to.
func (c *client) Access(ctx context.Context, key repokey.Key) (forge.Access, error) {
	r, repo, err := c.get(ctx, key)
	if err != nil {
		return forge.AccessNone, err
	}
	if repo.Key != key {
		return forge.AccessNone, fmt.Errorf("forge/bitbucketserver: %s was asked what this credential may do to %q and answered about %q", c.d.host, key, repo.Key)
	}
	type accessRung struct {
		perm  string
		level forge.Access
	}
	for _, rung := range []accessRung{{permAdmin, forge.AccessAdmin}, {permWrite, forge.AccessWrite}} {
		held, err := c.holds(ctx, r, key, rung.perm)
		if err != nil {
			return forge.AccessNone, err
		}
		if held {
			return rung.level, nil
		}
	}
	// Visible, and in neither listing: readable, and nothing more.
	return forge.AccessRead, nil
}

// holds reports whether the repository is in the listing of those this
// credential holds perm on. The listing filters by NAME, which is not unique
// across projects, so each entry is matched by its key.
func (c *client) holds(ctx context.Context, r repoJSON, key repokey.Key, perm string) (bool, error) {
	found := false
	err := walkPages(ctx, c, func(start int) request {
		return routeRepositories.at().with(url.Values{
			"name": {r.Name}, "permission": {perm},
			"start": {strconv.Itoa(start)}, "limit": {strconv.Itoa(pageLimit)},
		})
	}, func(values []repoJSON) bool {
		for _, v := range values {
			if k, err := c.d.keyOf(v.Project.Key, v.Slug); err == nil && k == key {
				found = true
				return false
			}
		}
		return true
	})
	return found, err
}

func (c *client) Repository(ctx context.Context, key repokey.Key) (forge.Repository, error) {
	_, repo, err := c.get(ctx, key)
	return repo, err
}

func (c *client) get(ctx context.Context, key repokey.Key) (repoJSON, forge.Repository, error) {
	at, err := c.d.coords(key)
	if err != nil {
		return repoJSON{}, forge.Repository{}, err
	}
	var r repoJSON
	if err := c.send(ctx, routeRepository.at(at.project, at.slug), nil, &r); err != nil {
		return repoJSON{}, forge.Repository{}, c.classify(err, key)
	}
	repo, err := c.repository(r, fmt.Sprintf("repository %q", key))
	if err != nil {
		return repoJSON{}, forge.Repository{}, err
	}
	if err := c.defaultBranch(ctx, r, &repo); err != nil {
		return repoJSON{}, forge.Repository{}, err
	}
	return r, repo, nil
}

// defaultBranch asks for it under the project and slug the FORGE named, not
// the ones the caller did. A repository with no commits has none, which
// Bitbucket answers 404 for, and the record then carries none.
func (c *client) defaultBranch(ctx context.Context, r repoJSON, repo *forge.Repository) error {
	type defaultBranchJSON struct {
		DisplayID string `json:"displayId"`
	}
	var b defaultBranchJSON
	err := c.send(ctx, routeDefaultBranch.at(r.Project.Key, r.Slug), nil, &b)
	var se *statusError
	if errors.As(err, &se) && se.Status == 404 {
		return nil
	}
	if err != nil {
		return c.classify(err, repokey.Key{})
	}
	repo.DefaultBranch = b.DisplayID
	return nil
}

// A repository answer carries no default branch, so each entry costs one more
// request: a record without one gives a builder nothing to check out.
func (c *client) ListRepositories(ctx context.Context, yield func(forge.Repository) bool) error {
	stopped := false
	var failed error
	err := walkPages(ctx, c, func(start int) request {
		return routeRepositories.at().with(url.Values{
			"permission": {permRead}, "start": {strconv.Itoa(start)}, "limit": {strconv.Itoa(pageLimit)},
		})
	}, func(values []repoJSON) bool {
		for i, r := range values {
			// One malformed entry is skipped rather than ending the listing.
			repo, err := c.repository(r, fmt.Sprintf("listing entry %d", i+1))
			if err != nil {
				continue
			}
			if err := c.defaultBranch(ctx, r, &repo); err != nil {
				failed = err
				return false
			}
			if !yield(repo) {
				stopped = true
				return false
			}
		}
		return true
	})
	switch {
	case failed != nil:
		return failed
	case stopped:
		return nil
	}
	return err
}

type pageOf[T any] struct {
	Values        []T  `json:"values"`
	IsLastPage    bool `json:"isLastPage"`
	Start         int  `json:"start"`
	NextPageStart *int `json:"nextPageStart"`
}

// walkPages walks a collection by "start". A next start that does not move
// forward is refused rather than followed, because a pager that trusts one
// either loops or skips.
func walkPages[T any](ctx context.Context, c *client, at func(start int) request, each func(values []T) bool) error {
	start := 0
	for page := 1; ; page++ {
		if page > maxPages {
			return fmt.Errorf("forge/bitbucketserver: %s is still answering a next page after %d of them; refusing to keep paging", c.d.host, maxPages)
		}
		var out pageOf[T]
		if err := c.send(ctx, at(start), nil, &out); err != nil {
			return c.classify(err, repokey.Key{})
		}
		if !each(out.Values) || out.IsLastPage || len(out.Values) == 0 {
			return nil
		}
		if out.NextPageStart == nil || *out.NextPageStart <= start {
			return fmt.Errorf("forge/bitbucketserver: %s answered a next page that does not follow this one, so the listing cannot be continued without guessing", c.d.host)
		}
		start = *out.NextPageStart
	}
}
