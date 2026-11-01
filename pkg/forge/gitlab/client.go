package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

var (
	_ forge.Client         = (*client)(nil)
	_ forge.Lister         = (*client)(nil)
	_ forge.Creator        = (*client)(nil)
	_ forge.Deleter        = (*client)(nil)
	_ forge.DeployKeys     = (*client)(nil)
	_ forge.CloneAddresses = (*client)(nil)
)

// client is this driver acting as one credential. It is stateless: every method
// takes the key it acts on, and nothing is remembered between calls.
type client struct {
	cfg *config
	api *http.Client
}

func (c *client) Host() string { return c.cfg.host }

// project omits the numeric id above all: the doctrine forbids it crossing this
// boundary, and every GitLab endpoint accepts it in place of a path.
type project struct {
	PathWithNamespace string `json:"path_with_namespace"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
	SSHURLToRepo      string `json:"ssh_url_to_repo"`
	DefaultBranch     string `json:"default_branch"`
	Visibility        string `json:"visibility"`
}

// Whoami is who this credential is, and it is the only honest token check GitLab
// offers: every other endpoint can fail for a second reason.
func (c *client) Whoami(ctx context.Context) (forge.Identity, error) {
	var user struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Email    string `json:"email"`
	}
	if _, err := c.get(ctx, routeCurrentUser.at(), nil, &user); err != nil {
		return forge.Identity{}, err
	}
	if user.Username == "" {
		return forge.Identity{}, fmt.Errorf("gitlab: %s answered the current-user endpoint with no username", c.cfg.host)
	}
	return forge.Identity{
		Login: user.Username,
		// Opaque, host-scoped, and rendered rather than kept numeric: nothing in
		// tau parses, orders or arithmetics it, and a USER id is not the
		// repository id the doctrine forbids. Email is empty on an instance that
		// hides it, which is not an error.
		Subject: strconv.FormatInt(user.ID, 10),
		Email:   user.Email,
	}, nil
}

// accessQuery names two permissions and fullPath, so the verdict can be bound to
// the question.
const accessQuery = `query($fullPath: ID!) {
  project(fullPath: $fullPath) {
    fullPath
    userPermissions { pushCode adminProject }
  }
}`

// Access is asked over GraphQL. The REST permissions hash must not be used: its
// project_access and group_access are null for an instance admin and for access
// through a shared or invited group, so it denies people who can push;
// userPermissions comes from the policy layer that decides the push itself. A
// project GraphQL will not resolve is *NotFoundError. The errors array is
// counted, not quoted: it is prose the instance composed.
func (c *client) Access(ctx context.Context, key repokey.Key) (forge.Access, error) {
	var answer struct {
		Data struct {
			Project *struct {
				FullPath        string `json:"fullPath"`
				UserPermissions struct {
					PushCode     bool `json:"pushCode"`
					AdminProject bool `json:"adminProject"`
				} `json:"userPermissions"`
			} `json:"project"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	// GraphQL answers 200 for failures, so anything unrecognised is an error, never
	// a level.
	if err := c.graphql(ctx, accessQuery, map[string]any{"fullPath": key.FullName()}, &answer); err != nil {
		return forge.AccessNone, err
	}
	if n := len(answer.Errors); n > 0 {
		return forge.AccessNone, fmt.Errorf("gitlab: %s answered the authorization query for %q with errors (%d) and no permissions", c.cfg.host, key, n)
	}
	if answer.Data.Project == nil {
		return forge.AccessNone, &forge.NotFoundError{Host: c.cfg.host, Key: key}
	}
	// Bound to the project asked about: GraphQL follows a rename's redirect route
	// internally, with no status this driver sees, and a transferred path may be
	// re-registered by anyone. The resolved name is not quoted. Case is no
	// disagreement: repokey lowercases and GitLab does not.
	if !strings.EqualFold(answer.Data.Project.FullPath, key.FullName()) {
		return forge.AccessNone, fmt.Errorf("gitlab: %s was asked what this credential may do to %q and answered about a different project", c.cfg.host, key)
	}

	switch permissions := answer.Data.Project.UserPermissions; {
	case permissions.AdminProject:
		// Maintainer and Owner. It is the rung webhooks and deploy keys need,
		// which on GitLab is strictly more than the rung a push needs.
		return forge.AccessAdmin, nil
	case permissions.PushCode:
		// Developer, and anyone above who is not a Maintainer.
		return forge.AccessWrite, nil
	}
	// The project resolved, so this credential may READ it — which is what a
	// public project grants everyone and what authorizes nothing.
	return forge.AccessRead, nil
}

// graphql posts one query. GitLab's GraphQL endpoint is not under /api/v4.
func (c *client) graphql(ctx context.Context, query string, variables map[string]any, out any) error {
	body := map[string]any{"query": query, "variables": variables}
	return c.send(ctx, routeAuthorization.at(), nil, body, out)
}

func (c *client) Repository(ctx context.Context, key repokey.Key) (forge.Repository, error) {
	var p project
	if _, err := c.get(ctx, routeProject.at(key.FullName()), &key, &p); err != nil {
		return forge.Repository{}, err
	}
	return c.cfg.repository(p, fmt.Sprintf("project %q", key.FullName()))
}

// repository derives the key from the clone URL the instance returned. A clone
// URL on another host is refused: the key would name a repository nothing can
// act on. Errors name asked, not the instance's path_with_namespace. The SSH URL
// passes unchecked: an instance may serve SSH on another name or port, which
// derives a different key.
func (c *config) repository(p project, asked string) (forge.Repository, error) {
	switch {
	case p.HTTPURLToRepo == "":
		return forge.Repository{}, fmt.Errorf("gitlab: %s described %s with no clone URL, so there is nothing to derive a key from", c.host, asked)
	case len(p.HTTPURLToRepo) > maxHostString:
		// Refused rather than parsed: a key derived from it would be stored,
		// indexed and printed by everything downstream, so the length an
		// instance picks would be a cost tau keeps paying.
		return forge.Repository{}, fmt.Errorf("gitlab: %s described %s with a %d-byte clone URL, which is not one", c.host, asked, len(p.HTTPURLToRepo))
	}
	key, err := repokey.Parse(p.HTTPURLToRepo)
	if err != nil {
		// The reason is not carried: repokey quotes what it was given, and a clone URL
		// with a token in its identity half would reach a log.
		return forge.Repository{}, fmt.Errorf("gitlab: %s reported a clone URL for %s that this layer cannot derive a key from", c.host, asked)
	}
	if key.Host != c.host {
		// key.Host is the one instance-chosen string left in this driver's errors:
		// which host it claimed is the whole mismatch. It is bounded, lowercased and
		// %q-escaped.
		return forge.Repository{}, fmt.Errorf("gitlab: %s reported %s on host %q; a key derived from it would name a repository on a host this deployment has no driver for", c.host, asked, key.Host)
	}
	return forge.Repository{
		Key:           key,
		HTTPS:         p.HTTPURLToRepo,
		SSH:           p.SSHURLToRepo,
		DefaultBranch: p.DefaultBranch,
		// GitLab has THREE visibilities and this field is a bool. "internal" —
		// readable by anyone signed in to the instance — is not public, so it
		// reads as private, which is the safe direction for a field that is
		// metadata and never authorization.
		Private: p.Visibility != "public",
	}, nil
}

// membership=true is the projects this credential holds a grant on, omitting
// public ones it merely reads. An entry whose clone URL cannot be keyed, or
// whose key Addressable refuses, is skipped, as the github driver does: an
// absence proves nothing.
func (c *client) ListRepositories(ctx context.Context, yield func(forge.Repository) bool) error {
	// GitLab pages with a header, and a broken or hostile pager that keeps
	// naming a next page would otherwise be an unbounded loop inside a call a
	// consumer thinks is one request.
	const maxPages = 1000

	page := "1"
	for pages := 0; page != ""; pages++ {
		if pages == maxPages {
			return fmt.Errorf("gitlab: %s is still naming a next page after %d of them; refusing to keep paging", c.cfg.host, maxPages)
		}
		rq := routeListProjects.at().with(url.Values{
			"membership": {"true"},
			"per_page":   {"100"},
			"page":       {page},
			"order_by":   {"id"},
			"sort":       {"asc"},
		})

		var listing []project
		r, err := c.get(ctx, rq, nil, &listing)
		if err != nil {
			return err
		}
		for i, p := range listing {
			repo, err := c.cfg.repository(p, fmt.Sprintf("entry %d of listing page %d", i+1, pages+1))
			if err != nil {
				continue
			}
			if c.cfg.addressable(repo.Key) != nil {
				continue
			}
			if !yield(repo) {
				return nil
			}
		}
		// X-Next-Page IS PUT IN THE NEXT REQUEST, so it is checked before it goes
		// anywhere: an instance that answers with prose there would otherwise
		// write it into a URL this driver composes and into any error quoting it.
		next := r.header.Get("X-Next-Page")
		if next != "" && !isPageNumber(next) {
			return fmt.Errorf("gitlab: %s named a next page that is not a page number", c.cfg.host)
		}
		if next == page {
			return fmt.Errorf("gitlab: %s named page %q as its own successor", c.cfg.host, page)
		}
		page = next
	}
	return nil
}

// isPageNumber reports whether a pager header is something this driver will put
// in a query. GitLab's X-Next-Page is empty on the last page and a decimal
// integer otherwise, so anything else is an instance writing into a request tau
// composes rather than naming a page.
func isPageNumber(s string) bool {
	if s == "" || len(s) > 9 {
		return false
	}
	for _, b := range []byte(s) {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

// GitLab creates under a namespace id, so this is two round trips: resolve the
// namespace, then create in it. The id is a namespace's, resolved per call and
// stored nowhere. The resolved namespace's path is compared with the one asked
// for, because the lookup follows a renamed path's redirect.
func (c *client) Create(ctx context.Context, key repokey.Key, opts forge.CreateOptions) (forge.Repository, error) {
	var namespace struct {
		ID       int64  `json:"id"`
		FullPath string `json:"full_path"`
	}
	if _, err := c.get(ctx, routeNamespace.at(key.Owner), nil, &namespace); err != nil {
		return forge.Repository{}, err
	}
	if namespace.ID == 0 {
		return forge.Repository{}, fmt.Errorf("gitlab: %s named no id for namespace %q", c.cfg.host, key.Owner)
	}
	if !strings.EqualFold(namespace.FullPath, key.Owner) {
		// The name it resolved to is not quoted: the instance chose it holding the
		// caller's credential.
		return forge.Repository{}, fmt.Errorf("gitlab: %s resolved namespace %q to a different one; creating there would put the repository somewhere other than where it was asked for", c.cfg.host, key.Owner)
	}

	visibility := "public"
	if opts.Private {
		visibility = "private"
	}
	body := map[string]any{
		"path":         key.Name,
		"name":         key.Name,
		"namespace_id": namespace.ID,
		"visibility":   visibility,
		"description":  opts.Description,
		// tau pushes the first commit itself. An initial README would be a
		// commit the caller did not ask for, in a history it is about to write.
		"initialize_with_readme": false,
	}

	var created project
	if err := c.send(ctx, routeCreateProject.at(), nil, body, &created); err != nil {
		return forge.Repository{}, err
	}
	repo, err := c.cfg.repository(created, fmt.Sprintf("project %q", key.FullName()))
	if err != nil {
		return forge.Repository{}, err
	}
	// GitLab derives a project's path from what it was sent and may not keep it, so
	// the project that came back must be the one asked for. The name it became is
	// not quoted.
	if repo.Key != key {
		return forge.Repository{}, fmt.Errorf("gitlab: %s was asked to create %q and created a project under a different path", c.cfg.host, key)
	}
	return repo, nil
}

// On GitLab nil means accepted for deletion: removal is asynchronous, and
// delayed deletion keeps the project readable for the retention period.
func (c *client) Delete(ctx context.Context, key repokey.Key) error {
	return c.send(ctx, routeDeleteProject.at(key.FullName()), &key, nil, nil)
}

// can_push is sent as false rather than left to the default. A GitLab deploy key
// is an instance-wide object: posting a public half that exists attaches it,
// and removing it detaches it from this project only.
func (c *client) InstallDeployKey(ctx context.Context, key repokey.Key, title, publicKey string) (forge.Handle, error) {
	body := map[string]any{"title": title, "key": publicKey, "can_push": false}
	var installed struct {
		ID      int64 `json:"id"`
		CanPush bool  `json:"can_push"`
	}
	if err := c.send(ctx, routeInstallDeployKey.at(key.FullName()), &key, body, &installed); err != nil {
		return "", err
	}
	if installed.ID == 0 {
		return "", fmt.Errorf("gitlab: %s installed a deploy key on %q and named no id for it, so nothing could ever remove it", c.cfg.host, key)
	}
	handle := forge.Handle(strconv.FormatInt(installed.ID, 10))
	if installed.CanPush {
		// Asked for read-only, given a key that can push. Whatever the reason —
		// the public half was already installed elsewhere as writable, or the
		// instance ignored the attribute — a standing write credential tau
		// issued is the exact thing this interface has no parameter for. Take it
		// back off, then say so.
		if err := c.RemoveDeployKey(ctx, key, handle); err != nil {
			return "", fmt.Errorf("gitlab: %s installed a deploy key on %q that can push, and it could not be removed again: %w", c.cfg.host, key, err)
		}
		return "", fmt.Errorf("gitlab: %s installed a deploy key on %q that can push; it has been removed", c.cfg.host, key)
	}
	return handle, nil
}

func (c *client) ListDeployKeys(ctx context.Context, key repokey.Key) ([]forge.InstalledDeployKey, error) {
	var out []forge.InstalledDeployKey
	err := c.pages(ctx, routeListDeployKeys, key, func(raw json.RawMessage) error {
		var got []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
			Key   string `json:"key"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			return err
		}
		for _, k := range got {
			if k.ID == 0 {
				return fmt.Errorf("gitlab: %s listed a deploy key on %q with no id", c.cfg.host, key)
			}
			out = append(out, forge.InstalledDeployKey{Handle: forge.Handle(strconv.FormatInt(k.ID, 10)), Title: k.Title, PublicKey: k.Key})
		}
		return nil
	})
	return out, err
}

// hookClient is a client of a signing instance, and the only client with push
// hooks. See signingDriver.
type hookClient struct{ *client }

func (c *hookClient) ListPushHooks(ctx context.Context, key repokey.Key) ([]forge.InstalledPushHook, error) {
	var out []forge.InstalledPushHook
	err := c.pages(ctx, routeListPushHooks, key, func(raw json.RawMessage) error {
		var got []struct {
			ID  int64  `json:"id"`
			URL string `json:"url"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			return err
		}
		for _, h := range got {
			if h.ID == 0 {
				return fmt.Errorf("gitlab: %s listed a hook on %q with no id", c.cfg.host, key)
			}
			out = append(out, forge.InstalledPushHook{Handle: forge.Handle(strconv.FormatInt(h.ID, 10)), Callback: h.URL})
		}
		return nil
	})
	return out, err
}

// pages walks a project sub-collection by X-Next-Page, with ListRepositories'
// bound and its checks on the header.
func (c *client) pages(ctx context.Context, rt *route, key repokey.Key, each func(json.RawMessage) error) error {
	const maxPages = 1000
	page := "1"
	for n := 0; page != ""; n++ {
		if n == maxPages {
			return fmt.Errorf("gitlab: %s is still naming a next page after %d of them; refusing to keep paging", c.cfg.host, maxPages)
		}
		var raw json.RawMessage
		r, err := c.get(ctx, rt.at(key.FullName()).with(url.Values{"per_page": {"100"}, "page": {page}}), &key, &raw)
		if err != nil {
			return err
		}
		if err := each(raw); err != nil {
			return err
		}
		next := r.header.Get("X-Next-Page")
		if next != "" && !isPageNumber(next) {
			return fmt.Errorf("gitlab: %s named a next page that is not a page number", c.cfg.host)
		}
		if next == page {
			return fmt.Errorf("gitlab: %s named page %q as its own successor", c.cfg.host, page)
		}
		page = next
	}
	return nil
}

// A 404 on .../deploy_keys/{id} is the same for no such project and no such key,
// so it names no repository: *NotFoundError would make a consumer drop a project
// that is there. Installing under a project that 404s is the project missing.
func (c *client) RemoveDeployKey(ctx context.Context, key repokey.Key, h forge.Handle) error {
	seg, err := handleSegment(h)
	if err != nil {
		return err
	}
	return c.send(ctx, routeRemoveDeployKey.at(key.FullName(), seg), nil, nil, nil)
}

// Registered for pushes only, signed: wire is the hook's signing_token, from
// which the instance derives a Standard Webhooks MAC. Never token, which GitLab
// replays verbatim and which binds nothing. wire is passed through unaltered.
func (c *hookClient) RegisterPushHook(ctx context.Context, key repokey.Key, callback *url.URL, wire string) (forge.Handle, error) {
	body := map[string]any{
		"url":                      callback.String(),
		"push_events":              true,
		"enable_ssl_verification":  true,
		"merge_requests_events":    false,
		"issues_events":            false,
		"tag_push_events":          false,
		"pipeline_events":          false,
		"repository_update_events": false,
		"signing_token":            wire,
	}

	var registered struct {
		ID int64 `json:"id"`
	}
	if err := c.send(ctx, routeRegisterPushHook.at(key.FullName()), &key, body, &registered); err != nil {
		return "", err
	}
	if registered.ID == 0 {
		return "", fmt.Errorf("gitlab: %s registered a hook on %q and named no id for it, so nothing could ever remove it", c.cfg.host, key)
	}
	return forge.Handle(strconv.FormatInt(registered.ID, 10)), nil
}

// RemovePushHook names no repository either, for the reason RemoveDeployKey
// gives.
func (c *hookClient) RemovePushHook(ctx context.Context, key repokey.Key, h forge.Handle) error {
	seg, err := handleSegment(h)
	if err != nil {
		return err
	}
	return c.send(ctx, routeRemovePushHook.at(key.FullName(), seg), nil, nil, nil)
}

// handleSegment: an empty handle addresses the collection (DELETE
// .../deploy_keys/ is every key), and InstallDeployKey returns one when it takes
// back a pushable key. A handle spanning a "/" names a resource under a
// collection nobody named, which the route cannot refuse since a "/" in a
// segment is how a project is addressed. Dot segments are refused here too,
// before the first request. It does not escape: the route does.
func handleSegment(h forge.Handle) (string, error) {
	if h == "" {
		return "", errors.New("gitlab: removing a resource needs the handle the instance gave it")
	}
	if strings.Contains(string(h), "/") {
		return "", fmt.Errorf("gitlab: the handle %q spans more than one path segment, and the route it fills has one; the removal would address a resource under a collection nobody named", bounded(string(h)))
	}
	if err := addressesItself(string(h)); err != nil {
		return "", fmt.Errorf("gitlab: the handle names no resource: %w", err)
	}
	return string(h), nil
}

// CloneAddress answers from the key alone and carries no credential. An instance
// serving SSH on another name or port derives a different key, which this
// method cannot know; use Repository's addresses there.
func (c *client) CloneAddress(key repokey.Key, over forge.Protocol) (forge.Address, error) {
	// repokey lets "?" and "#" through inside a segment, so a clone address goes
	// through the same escaping as a request path.
	escaped, err := escapedSegments(keySegments(key))
	if err != nil {
		return forge.Address{}, err
	}
	path := "/" + escaped + ".git"
	if over == forge.SSH {
		return forge.Address{
			URL: "ssh://git@" + c.cfg.host + path,
			// The account before the "@" is a host fact, and on GitLab it is
			// "git" for every instance including a self-managed one.
			SSHAccount: "git",
		}, nil
	}
	return forge.Address{
		URL: c.cfg.base.Scheme + "://" + c.cfg.host + path,
		// GitLab accepts any username beside a token over HTTP basic auth, and
		// documents "oauth2", which is the only one that works for BOTH an OAuth
		// access token and a personal one.
		HTTPUsername: "oauth2",
	}, nil
}
