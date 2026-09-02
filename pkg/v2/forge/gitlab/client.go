package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
)

// Compile-time face assertions for the client. One line per face, for the
// reason the driver's list gives.
var (
	_ forge.Client         = (*client)(nil)
	_ forge.Lister         = (*client)(nil)
	_ forge.Creator        = (*client)(nil)
	_ forge.Deleter        = (*client)(nil)
	_ forge.DeployKeys     = (*client)(nil)
	_ forge.PushHooks      = (*client)(nil)
	_ forge.CloneAddresses = (*client)(nil)
)

// client is this driver acting as one credential. It is stateless: every method
// takes the key it acts on, and nothing is remembered between calls.
type client struct {
	cfg *config
	api *http.Client
}

func (c *client) Host() string { return c.cfg.host }

// project is the slice of GitLab's project object this package reads. Everything
// absent is absent on purpose — the numeric id above all, which is the one field
// the doctrine forbids from crossing this boundary and which every GitLab
// endpoint would happily accept in place of a path.
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
	if _, err := c.get(ctx, c.cfg.endpoint("api", "v4", "user"), nil, &user); err != nil {
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

// accessQuery asks GitLab what this credential may do to one project. It names
// two permissions and nothing else, because every extra field is another thing
// that can be renamed out from under a running deployment.
const accessQuery = `query($fullPath: ID!) {
  project(fullPath: $fullPath) {
    userPermissions { pushCode adminProject }
  }
}`

// Access is the authorization question, and on GitLab it is asked over GraphQL.
//
// THE REST PERMISSIONS HASH MUST NOT BE USED, and this is the paragraph that
// stops the next person "simplifying" this back to one GET. GitLab's REST
// project entity exposes `permissions` as two members —
// project_access and group_access — which are the caller's DIRECT project
// membership and their membership of the project's own ancestor group. Both are
// null for an instance administrator, for a user whose access comes from a group
// the project was SHARED with, and for a member of a group invited into the
// project's group. Reading that hash therefore answers AccessNone for people who
// can genuinely push, and denying a legitimate writer is a bug that looks like a
// policy: it is reported as "tau says I have no access", investigated as a
// permissions problem on the user's side, and it never once looks like a wrong
// endpoint. userPermissions is computed by the same policy layer that decides
// whether the push itself is allowed, so it cannot drift from it.
//
// The three answers stay three. A project GraphQL will not resolve is
// *NotFoundError and not AccessNone — GitLab hides a project a caller may not
// see, so "no such project" and "you may not look" are one answer from outside
// and neither is a verdict a consumer may act on. An errors array is a plain
// error for the same reason.
//
// THE ERRORS ARRAY IS COUNTED AND NOT QUOTED. It is prose the instance composed,
// which makes it the same channel into tau's logs that fault refuses the response
// body for, and this endpoint is reached with the caller's own credential in a
// header. What is named instead is driver-chosen and enough to act on: the query
// is a const in this file, the count says the instance answered about it, and the
// instance's own log holds the sentence.
func (c *client) Access(ctx context.Context, key repokey.Key) (forge.Access, error) {
	var answer struct {
		Data struct {
			Project *struct {
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

	// GRAPHQL ANSWERS 200 FOR FAILURES. The taxonomy is written against HTTP
	// statuses, so a driver whose authorization question does not travel over
	// REST has to map a second failure channel by hand — and the safe direction
	// is that anything unrecognised is an error, never a level.
	if err := c.graphql(ctx, accessQuery, map[string]any{"fullPath": key.FullName()}, &answer); err != nil {
		return forge.AccessNone, err
	}
	if n := len(answer.Errors); n > 0 {
		return forge.AccessNone, fmt.Errorf("gitlab: %s answered the authorization query for %q with errors (%d) and no permissions", c.cfg.host, key, n)
	}
	if answer.Data.Project == nil {
		return forge.AccessNone, &forge.NotFoundError{Host: c.cfg.host, Key: key}
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
	return c.send(ctx, http.MethodPost, c.cfg.endpoint("api", "graphql"), nil, body, out)
}

// Repository is what only the instance knows.
func (c *client) Repository(ctx context.Context, key repokey.Key) (forge.Repository, error) {
	var p project
	if _, err := c.get(ctx, c.cfg.endpoint("api", "v4", "projects", key.FullName()), &key, &p); err != nil {
		return forge.Repository{}, err
	}
	return c.cfg.repository(p)
}

// repository turns GitLab's project into tau's, DERIVING THE KEY FROM THE CLONE
// URL the instance returned rather than echoing the one that was asked for. That
// is what makes "the instance and tau agree which repository this is" a checked
// fact: a rename, a redirect or a case-folding disagreement shows up here
// instead of three layers later.
//
// A clone URL on another host is refused. It is not a hypothetical — an instance
// behind a proxy that was told the wrong external URL emits exactly that — and
// the key derived from it would file the repository under a host this deployment
// has no driver for, which is a repository nothing can ever act on again.
//
// THE SSH URL IS PASSED THROUGH UNCHECKED, and that is a deliberate, flagged
// asymmetry. A self-managed instance routinely serves SSH on another port or
// another name (ssh.acme.example:2222), which is a perfectly good clone address
// and a DIFFERENT repokey key — because the host is part of the identity. This
// driver reports what the instance said rather than inventing agreement; the
// consequence is that on such an instance the two addresses of one repository
// derive two keys, which is a property of the doctrine and not of this method.
func (c *config) repository(p project) (forge.Repository, error) {
	if p.HTTPURLToRepo == "" {
		return forge.Repository{}, fmt.Errorf("gitlab: %s described project %q with no clone URL, so there is nothing to derive a key from", c.host, p.PathWithNamespace)
	}
	key, err := repokey.Parse(p.HTTPURLToRepo)
	if err != nil {
		return forge.Repository{}, fmt.Errorf("gitlab: %s reported a clone URL that is not one: %w", c.host, err)
	}
	if key.Host != c.host {
		return forge.Repository{}, fmt.Errorf("gitlab: %s reported clone URL on host %q; a key derived from it would name a repository on a host this deployment has no driver for", c.host, key.Host)
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

// ListRepositories yields every project this credential is a member of.
//
// membership=true is the closest GitLab has to the weakest true contract: it is
// the projects this credential holds a grant on, and it OMITS public projects
// the caller merely can read. An absence therefore proves nothing, which is what
// the interface already says about every forge's listing.
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
		u := c.cfg.endpoint("api", "v4", "projects")
		u.RawQuery = url.Values{
			"membership": {"true"},
			"per_page":   {"100"},
			"page":       {page},
			"order_by":   {"id"},
			"sort":       {"asc"},
		}.Encode()

		var listing []project
		r, err := c.get(ctx, u, nil, &listing)
		if err != nil {
			return err
		}
		for _, p := range listing {
			repo, err := c.cfg.repository(p)
			if err != nil {
				// A listing that quietly drops the entry it could not read is a
				// listing whose absences a consumer cannot interpret.
				return err
			}
			if !yield(repo) {
				return nil
			}
		}
		next := r.header.Get("X-Next-Page")
		if next == page {
			return fmt.Errorf("gitlab: %s named page %q as its own successor", c.cfg.host, page)
		}
		page = next
	}
	return nil
}

// Create makes a project AT THE KEY IT WILL BE ADDRESSED BY.
//
// GITLAB CREATES UNDER A NAMESPACE ID, NOT UNDER A PATH, so this is two round
// trips: resolve the namespace, then create in it. The integer is a NAMESPACE id
// — a group's or a user's — never a repository id; it is resolved per call, it is
// stored nowhere, and it never appears in a signature. What it does mean is that
// creation is not a pure path operation on this forge, and that is a fact about
// GitLab worth reading here rather than discovering in a trace.
//
// The resolved namespace's own full path is compared with the one that was
// asked for, because GitLab's namespace lookup FOLLOWS A REDIRECT FROM A RENAMED
// PATH: without the comparison, creating in "acme" after acme was renamed
// silently creates in whatever it is called now.
func (c *client) Create(ctx context.Context, key repokey.Key, opts forge.CreateOptions) (forge.Repository, error) {
	var namespace struct {
		ID       int64  `json:"id"`
		FullPath string `json:"full_path"`
	}
	if _, err := c.get(ctx, c.cfg.endpoint("api", "v4", "namespaces", key.Owner), nil, &namespace); err != nil {
		return forge.Repository{}, err
	}
	if namespace.ID == 0 {
		return forge.Repository{}, fmt.Errorf("gitlab: %s named no id for namespace %q", c.cfg.host, key.Owner)
	}
	if !strings.EqualFold(namespace.FullPath, key.Owner) {
		return forge.Repository{}, fmt.Errorf("gitlab: %s resolved namespace %q to %q; creating there would put the repository somewhere other than where it was asked for", c.cfg.host, key.Owner, namespace.FullPath)
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
	if err := c.send(ctx, http.MethodPost, c.cfg.endpoint("api", "v4", "projects"), nil, body, &created); err != nil {
		return forge.Repository{}, err
	}
	return c.cfg.repository(created)
}

// Delete removes a project.
//
// ON GITLAB, NIL MEANS "ACCEPTED FOR DELETION" AND NOT ALWAYS "GONE". The API
// answers 202 and the removal is asynchronous, and an instance or group with
// delayed deletion configured keeps the project readable, with a deletion date
// on it, for the retention period. A consumer must therefore not read a
// subsequent successful Repository as this call having failed — which is exactly
// the shape of assumption a GitHub-only integration acquires without noticing.
func (c *client) Delete(ctx context.Context, key repokey.Key) error {
	return c.send(ctx, http.MethodDelete, c.cfg.endpoint("api", "v4", "projects", key.FullName()), &key, nil, nil)
}

// InstallDeployKey installs a read-only clone credential.
//
// can_push IS SENT EXPLICITLY AS FALSE rather than left out. GitLab's default is
// already false, and relying on a default is precisely how v1 ended up
// installing a write key on every repository it ever registered: it set a title
// and a key, and its SDK's zero value for read-only was false. A value that
// matters is written down.
//
// A GITLAB DEPLOY KEY IS A GLOBAL OBJECT SHARED BY PROJECTS. Posting a public
// half that already exists on the instance attaches the existing key rather than
// making a new one, and removing it detaches it from THIS project while it stays
// enabled wherever else it was. So the handle names an instance-wide key and the
// pair install/remove is really enable/disable-here — which is the right
// per-repository behaviour for tau and a different mechanism from GitHub's.
func (c *client) InstallDeployKey(ctx context.Context, key repokey.Key, title, publicKey string) (forge.Handle, error) {
	body := map[string]any{"title": title, "key": publicKey, "can_push": false}
	var installed struct {
		ID      int64 `json:"id"`
		CanPush bool  `json:"can_push"`
	}
	u := c.cfg.endpoint("api", "v4", "projects", key.FullName(), "deploy_keys")
	if err := c.send(ctx, http.MethodPost, u, &key, body, &installed); err != nil {
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

func (c *client) RemoveDeployKey(ctx context.Context, key repokey.Key, h forge.Handle) error {
	u := c.cfg.endpoint("api", "v4", "projects", key.FullName(), "deploy_keys", string(h))
	return c.send(ctx, http.MethodDelete, u, &key, nil, nil)
}

// RegisterPushHook registers a webhook for pushes only.
//
// THE ATTRIBUTE THE WIRE STRING GOES IN DEPENDS ON THE SCHEME, and that is the
// generalization this interface bought. GitLab's long-standing procedure takes
// `token` and replays it verbatim in a header; Standard Webhooks signing takes
// `signing_token` and derives a MAC from it. One forge, two attributes, two
// procedures — which is why the driver names a scheme and the core owns both
// derivation and verification, and why the scheme is stored with the hook rather
// than inferred from the host later.
//
// The driver never sees the secret. It is handed the already-derived wire string
// and passes it through unaltered; re-encoding it here would register a hook
// cleanly and then reject every delivery it ever received.
func (c *client) RegisterPushHook(ctx context.Context, key repokey.Key, callback *url.URL, wire string) (forge.Handle, error) {
	body := map[string]any{
		"url":                      callback.String(),
		"push_events":              true,
		"enable_ssl_verification":  true,
		"merge_requests_events":    false,
		"issues_events":            false,
		"tag_push_events":          false,
		"pipeline_events":          false,
		"repository_update_events": false,
	}
	switch c.cfg.scheme {
	case forge.SchemeStandardWebhooks:
		body["signing_token"] = wire
	default:
		body["token"] = wire
	}

	var registered struct {
		ID int64 `json:"id"`
	}
	u := c.cfg.endpoint("api", "v4", "projects", key.FullName(), "hooks")
	if err := c.send(ctx, http.MethodPost, u, &key, body, &registered); err != nil {
		return "", err
	}
	if registered.ID == 0 {
		return "", fmt.Errorf("gitlab: %s registered a hook on %q and named no id for it, so nothing could ever remove it", c.cfg.host, key)
	}
	return forge.Handle(strconv.FormatInt(registered.ID, 10)), nil
}

func (c *client) RemovePushHook(ctx context.Context, key repokey.Key, h forge.Handle) error {
	u := c.cfg.endpoint("api", "v4", "projects", key.FullName(), "hooks", string(h))
	return c.send(ctx, http.MethodDelete, u, &key, nil, nil)
}

// CloneAddress is the address a clone of one key uses on this instance, and the
// account it authenticates as. It carries no credential: pkg/git splices a token
// into the remote URL, which then lands in .git/config, in process arguments and
// in every error quoting the remote.
//
// IT ANSWERS FROM THE KEY ALONE — no context, no request — and on a self-managed
// instance that is a real limit rather than an efficiency. Such an instance may
// serve SSH on another port or another name, which Repository would report
// truthfully from ssh_url_to_repo and which this method cannot know. The
// interface has nowhere to put that answer anyway: forge.Address.URL has to
// re-derive the key, and a different SSH host derives a different key. A
// deployment whose instance moves SSH must use Repository's addresses.
func (c *client) CloneAddress(key repokey.Key, over forge.Protocol) (forge.Address, error) {
	if over == forge.SSH {
		return forge.Address{
			URL: "ssh://git@" + c.cfg.host + "/" + key.FullName() + ".git",
			// The account before the "@" is a host fact, and on GitLab it is
			// "git" for every instance including a self-managed one.
			SSHAccount: "git",
		}, nil
	}
	return forge.Address{
		URL: c.cfg.base.Scheme + "://" + c.cfg.host + "/" + key.FullName() + ".git",
		// GitLab accepts any username beside a token over HTTP basic auth, and
		// documents "oauth2", which is the only one that works for BOTH an OAuth
		// access token and a personal one.
		HTTPUsername: "oauth2",
	}, nil
}
