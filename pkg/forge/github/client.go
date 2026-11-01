package github

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

// listPageSize is GitHub's maximum. The pager counts pages and stops on a short
// one rather than following the Link header, a URL the host chooses.
const listPageSize = 100

// There is no parameter for it: a builder only clones, and a write key nothing
// removes is a standing credential.
const deployKeyReadOnly = true

// Whoami is who this credential belongs to, and it doubles as the only honest
// check that the credential is still good.
func (c *client) Whoami(ctx context.Context) (forge.Identity, error) {
	var u userJSON
	if err := c.send(ctx, routeCurrentUser.at(), nil, &u); err != nil {
		return forge.Identity{}, c.classify(err, repokey.Key{})
	}
	if u.Login == "" {
		return forge.Identity{}, errors.New("forge/github: the forge answered with no login, so this credential belongs to nobody")
	}
	return forge.Identity{
		Login: u.Login,
		// Subject is GitHub's user number rendered as a string and treated as
		// opaque from here on: nothing in this package parses, orders or
		// compares it, and nothing here persists it.
		Subject: strconv.FormatInt(u.ID, 10),
		Email:   u.Email,
	}, nil
}

type userJSON struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

// GitHub returns the caller's permissions with the repository, so this is one
// request. The verdict is bound to the key asked about: permissions is nameless,
// and a transferred name may be re-registered by anyone.
func (c *client) Access(ctx context.Context, key repokey.Key) (forge.Access, error) {
	r, repo, err := c.get(ctx, key)
	if err != nil {
		return forge.AccessNone, err
	}
	if repo.Key != key {
		return forge.AccessNone, fmt.Errorf("forge/github: %s was asked what this credential may do to %q and answered about %q", c.d.host, key, repo.Key)
	}
	return r.Permissions.access(), nil
}

// Repository reports, so it does not bind: a record names itself, and a rename
// arrives as a different key. A verdict carries no name, so Access must bind.
func (c *client) Repository(ctx context.Context, key repokey.Key) (forge.Repository, error) {
	_, repo, err := c.get(ctx, key)
	if err != nil {
		return forge.Repository{}, err
	}
	return repo, nil
}

// get derives the repository once for Access and Repository, which differ only
// in what they do about a disagreement. Case is none: repokey lowercases.
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

// It passes no affiliation, GitHub's vocabulary, so GitHub's default applies. A
// repository whose clone URL cannot be named or whose shape cannot be addressed
// is skipped.
func (c *client) ListRepositories(ctx context.Context, yield func(forge.Repository) bool) error {
	// A forge always answering a full page would loop forever; a hundred thousand
	// repositories is more than any credential reaches.
	const maxPages = 1000

	for page := 1; ; page++ {
		if page > maxPages {
			return fmt.Errorf("forge/github: %s is still answering a full page after %d of them; refusing to keep paging", c.d.host, maxPages)
		}
		var out []repoJSON
		rq := routeListRepositories.at().with(url.Values{
			"per_page": {strconv.Itoa(listPageSize)},
			"page":     {strconv.Itoa(page)},
		})
		if err := c.send(ctx, rq, nil, &out); err != nil {
			return c.classify(err, repokey.Key{})
		}
		for i, r := range out {
			repo, err := c.repository(r, fmt.Sprintf("entry %d of listing page %d", i+1, page))
			if err != nil {
				continue
			}
			if c.d.Addressable(repo.Key) != nil {
				continue
			}
			if !yield(repo) {
				return nil
			}
		}
		// A short page is the last page. Stopping on the count rather than on a
		// Link header keeps termination a property of this loop.
		if len(out) < listPageSize {
			return nil
		}
	}
}

type createRepoRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private"`
}

// Create makes the repository at its key, namespace included. Which endpoint
// applies depends on who the credential is: POST /orgs/{me}/repos is a 404 for a
// user.
func (c *client) Create(ctx context.Context, key repokey.Key, opts forge.CreateOptions) (forge.Repository, error) {
	me, err := c.Whoami(ctx)
	if err != nil {
		return forge.Repository{}, err
	}

	rq := routeCreateUnderOrg.at(key.Owner)
	if strings.EqualFold(key.Owner, me.Login) {
		rq = routeCreateOwnedByMe.at()
	}

	var r repoJSON
	body := createRepoRequest{Name: key.Name, Description: opts.Description, Private: opts.Private}
	if err := c.send(ctx, rq, body, &r); err != nil {
		return forge.Repository{}, c.classifyResource(err)
	}

	repo, err := c.repository(r, fmt.Sprintf("repository %q", key))
	if err != nil {
		return forge.Repository{}, err
	}
	// A repository made somewhere nobody asked for would be filed under a name that
	// does not name it, one anyone may register.
	if repo.Key != key {
		return forge.Repository{}, fmt.Errorf("forge/github: asked for %q and the forge created %q", key, repo.Key)
	}
	return repo, nil
}

func (c *client) Delete(ctx context.Context, key repokey.Key) error {
	if err := c.send(ctx, routeDeleteRepository.at(key.Owner, key.Name), nil, nil); err != nil {
		return c.classify(err, key)
	}
	return nil
}

type createKeyRequest struct {
	Title    string `json:"title"`
	Key      string `json:"key"`
	ReadOnly bool   `json:"read_only"`
}

type resourceJSON struct {
	ID int64 `json:"id"`
}

// InstallDeployKey installs a READ-ONLY machine credential. See
// deployKeyReadOnly: there is no parameter, and there is none on purpose.
func (c *client) InstallDeployKey(ctx context.Context, key repokey.Key, title, publicKey string) (forge.Handle, error) {
	var out resourceJSON
	body := createKeyRequest{Title: title, Key: publicKey, ReadOnly: deployKeyReadOnly}
	if err := c.send(ctx, routeInstallDeployKey.at(key.Owner, key.Name), body, &out); err != nil {
		return "", c.classify(err, key)
	}
	return handle(out.ID)
}

func (c *client) RemoveDeployKey(ctx context.Context, key repokey.Key, h forge.Handle) error {
	seg, err := handleSegment(h)
	if err != nil {
		return err
	}
	if err := c.send(ctx, routeRemoveDeployKey.at(key.Owner, key.Name, seg), nil, nil); err != nil {
		return c.classifyResource(err)
	}
	return nil
}

type deployKeyJSON struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Key   string `json:"key"`
}

func (c *client) ListDeployKeys(ctx context.Context, key repokey.Key) ([]forge.InstalledDeployKey, error) {
	var out []forge.InstalledDeployKey
	err := pages(c.d.host, func(page int) (int, error) {
		var got []deployKeyJSON
		rq := routeListDeployKeys.at(key.Owner, key.Name).with(pageQuery(page))
		if err := c.send(ctx, rq, nil, &got); err != nil {
			return 0, c.classify(err, key)
		}
		for _, k := range got {
			h, err := handle(k.ID)
			if err != nil {
				return 0, err
			}
			out = append(out, forge.InstalledDeployKey{Handle: h, Title: k.Title, PublicKey: k.Key})
		}
		return len(got), nil
	})
	return out, err
}

type hookJSON struct {
	ID     int64 `json:"id"`
	Config struct {
		URL string `json:"url"`
	} `json:"config"`
}

func (c *client) ListPushHooks(ctx context.Context, key repokey.Key) ([]forge.InstalledPushHook, error) {
	var out []forge.InstalledPushHook
	err := pages(c.d.host, func(page int) (int, error) {
		var got []hookJSON
		rq := routeListPushHooks.at(key.Owner, key.Name).with(pageQuery(page))
		if err := c.send(ctx, rq, nil, &got); err != nil {
			return 0, c.classify(err, key)
		}
		for _, h := range got {
			hd, err := handle(h.ID)
			if err != nil {
				return 0, err
			}
			out = append(out, forge.InstalledPushHook{Handle: hd, Callback: h.Config.URL})
		}
		return len(got), nil
	})
	return out, err
}

func pageQuery(page int) url.Values {
	return url.Values{"per_page": {strconv.Itoa(listPageSize)}, "page": {strconv.Itoa(page)}}
}

// pages runs fetch on page 1, 2, ... until a short page, under the same bound
// ListRepositories keeps.
func pages(host string, fetch func(page int) (int, error)) error {
	const maxPages = 1000
	for page := 1; ; page++ {
		if page > maxPages {
			return fmt.Errorf("forge/github: %s is still answering a full page after %d of them; refusing to keep paging", host, maxPages)
		}
		n, err := fetch(page)
		if err != nil {
			return err
		}
		if n < listPageSize {
			return nil
		}
	}
}

type createHookRequest struct {
	Name   string     `json:"name"`
	Active bool       `json:"active"`
	Events []string   `json:"events"`
	Config hookConfig `json:"config"`
}

type hookConfig struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	InsecureSSL string `json:"insecure_ssl"`
	Secret      string `json:"secret"`
}

// RegisterPushHook subscribes to pushes and nothing else, with insecure_ssl "0"
// since the callback receives secret-bearing deliveries. wire is passed through
// verbatim.
func (c *client) RegisterPushHook(ctx context.Context, key repokey.Key, callback *url.URL, wire string) (forge.Handle, error) {
	var out resourceJSON
	body := createHookRequest{
		Name:   "web",
		Active: true,
		Events: []string{"push"},
		Config: hookConfig{
			URL:         callback.String(),
			ContentType: "json",
			InsecureSSL: "0",
			Secret:      wire,
		},
	}
	if err := c.send(ctx, routeRegisterPushHook.at(key.Owner, key.Name), body, &out); err != nil {
		return "", c.classify(err, key)
	}
	return handle(out.ID)
}

func (c *client) RemovePushHook(ctx context.Context, key repokey.Key, h forge.Handle) error {
	seg, err := handleSegment(h)
	if err != nil {
		return err
	}
	if err := c.send(ctx, routeRemovePushHook.at(key.Owner, key.Name, seg), nil, nil); err != nil {
		return c.classifyResource(err)
	}
	return nil
}

// handle is the id GitHub gave a resource IT owns, as an opaque string. It is a
// string because Bitbucket's is a UUID; nothing in tau parses or orders one,
// and this is the only place the number is rendered.
func handle(id int64) (forge.Handle, error) {
	if id == 0 {
		return "", errors.New("forge/github: the forge returned no id for the resource it created, so nothing could ever remove it")
	}
	return forge.Handle(strconv.FormatInt(id, 10)), nil
}

// handleSegment: a handle names one resource under one collection, so one
// segment. An empty one addresses the collection, and one spanning a "/"
// addresses a resource under a collection nobody named, with no dot segment for
// the route layer to catch; dot segments are refused here too, before the first
// request spends the credential. It does not escape: the route does.
func handleSegment(h forge.Handle) (string, error) {
	if h == "" {
		return "", errors.New("forge/github: removing a resource needs the handle the forge gave it")
	}
	if strings.Contains(string(h), "/") {
		return "", fmt.Errorf("forge/github: the handle %q spans more than one path segment, and the route it fills has one; the removal would address a resource under a collection nobody named", bounded(string(h)))
	}
	if err := addressesItself(string(h)); err != nil {
		return "", fmt.Errorf("forge/github: the handle names no resource: %w", err)
	}
	return string(h), nil
}

// CloneAddress is derived from the key with no request and no credential in the
// URL. On a host with a port only one of the two addresses can be right, since
// the key's port came from whichever transport named the repository;
// Repository() returns the forge's own addresses.
func (c *client) CloneAddress(key repokey.Key, over forge.Protocol) (forge.Address, error) {
	// repokey lets "?" and "#" through inside a segment, so a clone address goes
	// through the same escaping as a request path.
	escaped, err := escapedSegments([]string{key.Owner, key.Name})
	if err != nil {
		return forge.Address{}, err
	}
	path := "/" + escaped + ".git"
	if over == forge.SSH {
		// "git" is the account every GitHub answers ssh on, and it is a host
		// fact rather than an identity: the key that authenticates is the
		// caller's.
		return forge.Address{URL: "ssh://git@" + c.d.host + path, SSHAccount: "git"}, nil
	}
	// GitHub accepts any username with a token as the password over HTTPS, but
	// "x-access-token" is the one it documents, and a driver that picked its own
	// is a driver whose remotes read differently from everybody else's.
	return forge.Address{URL: "https://" + c.d.host + path, HTTPUsername: "x-access-token"}, nil
}
