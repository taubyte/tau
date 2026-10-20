package bitbucket

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

// listPageSize is Bitbucket's own maximum pagelen.
const listPageSize = 100

// maxPages bounds a listing a forge never ends.
const maxPages = 1000

// Whoami says who this credential is.
//
// Subject is the account's UUID, the one identifier Bitbucket never reassigns;
// a username can be changed and Bitbucket stopped treating it as an identifier.
// Bitbucket keeps the email behind a separate scope and endpoint, so Email is
// empty rather than guessed.
func (c *client) Whoami(ctx context.Context) (forge.Identity, error) {
	var u userJSON
	if err := c.send(ctx, routeCurrentUser.at(), nil, &u); err != nil {
		return forge.Identity{}, c.classify(err, repokey.Key{})
	}
	if u.Username == "" || u.UUID == "" {
		return forge.Identity{}, errors.New("forge/bitbucket: the forge answered with no username or uuid, so this credential belongs to nobody")
	}
	return forge.Identity{Login: u.Username, Subject: u.UUID}, nil
}

type userJSON struct {
	Username string `json:"username"`
	UUID     string `json:"uuid"`
}

// Access is the authorization question. The repository is fetched first —
// which is what tells a missing or hidden repository (404, NotFoundError) from
// a visible one — and then the caller's grant is asked for by the full name
// the FORGE spelled, never the one the caller did.
func (c *client) Access(ctx context.Context, key repokey.Key) (forge.Access, error) {
	r, repo, err := c.get(ctx, key)
	if err != nil {
		return forge.AccessNone, err
	}
	if repo.Key != key {
		return forge.AccessNone, fmt.Errorf("forge/bitbucket: %s was asked what this credential may do to %q and answered about %q", c.d.host, key, repo.Key)
	}
	full := r.FullName
	if !strings.EqualFold(full, key.FullName()) || strings.ContainsAny(full, `"\`) {
		return forge.AccessNone, fmt.Errorf("forge/bitbucket: %s named the repository %q as %q, which is not the repository its clone link names", c.d.host, key, bounded(full))
	}

	var out struct {
		Values []struct {
			Permission string `json:"permission"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		} `json:"values"`
	}
	q := url.Values{"q": {`repository.full_name="` + full + `"`}}
	if err := c.send(ctx, routePermission.at().with(q), nil, &out); err != nil {
		return forge.AccessNone, c.classify(err, repokey.Key{})
	}

	// No grant on a repository this credential could read is a public
	// repository: readable, and nothing more.
	level := forge.AccessRead
	for _, v := range out.Values {
		if !strings.EqualFold(v.Repository.FullName, full) {
			return forge.AccessNone, fmt.Errorf("forge/bitbucket: %s was asked for the grant on %q and answered about %q", c.d.host, key, bounded(v.Repository.FullName))
		}
		var got forge.Access
		switch v.Permission {
		case "admin":
			got = forge.AccessAdmin
		case "write":
			got = forge.AccessWrite
		case "read":
			got = forge.AccessRead
		default:
			return forge.AccessNone, fmt.Errorf("forge/bitbucket: %s answered a permission %q this driver does not know", c.d.host, bounded(v.Permission))
		}
		level = max(level, got)
	}
	return level, nil
}

// Repository is what only the forge knows, with the key derived from its answer.
func (c *client) Repository(ctx context.Context, key repokey.Key) (forge.Repository, error) {
	_, repo, err := c.get(ctx, key)
	if err != nil {
		return forge.Repository{}, err
	}
	return repo, nil
}

func (c *client) get(ctx context.Context, key repokey.Key) (repoJSON, forge.Repository, error) {
	var r repoJSON
	if err := c.send(ctx, routeRepository.at(key.Owner, key.Name), nil, &r); err != nil {
		return repoJSON{}, forge.Repository{}, c.classify(err, key)
	}
	repo, err := c.repository(r, fmt.Sprintf("repository %q", key))
	if err != nil {
		return repoJSON{}, forge.Repository{}, err
	}
	return r, repo, nil
}

// ListRepositories yields every repository this credential is a member of.
// Public repositories it was never granted are absent, which the Lister
// contract already says proves nothing.
func (c *client) ListRepositories(ctx context.Context, yield func(forge.Repository) bool) error {
	stopped := false
	err := walkPages(ctx, c, func(page int) request {
		return routeListRepositories.at().with(url.Values{
			"role": {"member"}, "pagelen": {strconv.Itoa(listPageSize)}, "page": {strconv.Itoa(page)},
		})
	}, repokey.Key{}, func(page int, values []repoJSON) bool {
		for i, r := range values {
			// One malformed entry is skipped rather than ending the listing:
			// everything yielded is readable, and an absence proves nothing.
			repo, err := c.repository(r, fmt.Sprintf("entry %d of listing page %d", i+1, page))
			if err != nil {
				continue
			}
			if c.d.Addressable(repo.Key) != nil {
				continue
			}
			if !yield(repo) {
				stopped = true
				return false
			}
		}
		return true
	})
	if stopped {
		return nil
	}
	return err
}

// pageOf is one page of a Bitbucket collection.
type pageOf[T any] struct {
	Values []T    `json:"values"`
	Next   string `json:"next"`
}

// pages walks a Bitbucket collection.
//
// IT DOES NOT REQUEST THE "next" URL. That URL is a string the forge wrote, and
// requesting it would be a request this driver did not compose, outside its
// route table. What "next" is trusted for is one thing — that there IS a next
// page, and its number — and it must say so in the shape this driver asked in:
// the same path, and a page one greater. Anything else is refused rather than
// guessed at, because a pager that guesses either truncates a listing silently
// or asks for pages forever.
//
// each returning false stops the walk and is not an error.
func walkPages[T any](ctx context.Context, c *client, at func(page int) request, key repokey.Key, each func(page int, values []T) bool) error {
	for page := 1; ; page++ {
		if page > maxPages {
			return fmt.Errorf("forge/bitbucket: %s is still answering a next page after %d of them; refusing to keep paging", c.d.host, maxPages)
		}
		rq := at(page)
		var out pageOf[T]
		if err := c.send(ctx, rq, nil, &out); err != nil {
			return c.classify(err, key)
		}
		if !each(page, out.Values) || out.Next == "" || len(out.Values) == 0 {
			return nil
		}
		if err := c.isNextPage(rq, out.Next, page+1); err != nil {
			return err
		}
	}
}

// isNextPage checks that a "next" link names page want of the request rq, and
// nothing else.
func (c *client) isNextPage(rq request, next string, want int) error {
	refuse := fmt.Errorf("forge/bitbucket: %s answered a next page this driver did not ask in the shape of, so the listing cannot be continued without guessing", c.d.host)
	u, err := url.Parse(next)
	if err != nil {
		return refuse
	}
	path, err := rq.path()
	if err != nil {
		return err
	}
	if u.Host != c.d.apiHost || u.EscapedPath() != c.d.base.EscapedPath()+path {
		return refuse
	}
	q := u.Query()
	for name := range q {
		if name != "page" && rq.query.Get(name) != q.Get(name) {
			return refuse
		}
	}
	if q.Get("page") != strconv.Itoa(want) {
		return refuse
	}
	return nil
}
