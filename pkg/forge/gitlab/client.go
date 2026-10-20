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

// Compile-time face assertions for the client. One line per face, for the
// reason the driver's list gives.
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

// accessQuery asks GitLab what this credential may do to one project. It names
// two permissions and the project's own path, and nothing else, because every
// extra field is another thing that can be renamed out from under a running
// deployment.
//
// fullPath IS THE ANSWER'S NAME, and it is asked for so that the verdict can be
// bound to the question. See Access.
const accessQuery = `query($fullPath: ID!) {
  project(fullPath: $fullPath) {
    fullPath
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
	// AND IT IS BOUND TO THE PROJECT IT WAS ASKED ABOUT. userPermissions is a
	// nameless field: it describes whatever project the answer describes, so
	// reading it as the verdict for a different key answers a question nobody
	// asked. The channel is not the 3xx noRedirect refuses — GraphQL resolves a
	// project internally, so a REDIRECT ROUTE left behind by a rename or a
	// TRANSFER is followed with no status this driver ever sees, and a
	// transferred path is free for anyone to re-register the moment it is
	// transferred. "admin on whatever victim/pay currently points at" is then a
	// grant on a path an attacker may already hold.
	//
	// The name it resolved TO is not quoted, for the reason repository gives:
	// the instance chose that string while holding the caller's credential.
	// Case is not a disagreement: repokey lowercases and GitLab does not.
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

// Repository is what only the instance knows.
func (c *client) Repository(ctx context.Context, key repokey.Key) (forge.Repository, error) {
	var p project
	if _, err := c.get(ctx, routeProject.at(key.FullName()), &key, &p); err != nil {
		return forge.Repository{}, err
	}
	return c.cfg.repository(p, fmt.Sprintf("project %q", key.FullName()))
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
// asked IS WHAT TAU CALLED THIS PROJECT, and every error below names it rather
// than the path_with_namespace the instance sent. The instance chose that string
// while holding the caller's credential, so quoting it back is the credential
// channel fault was just closed for; a caller always has a name of its own —
// the key it asked about, or the coordinates of the listing entry — and it is
// the more useful half of the mismatch anyway.
//
// What survives is one instance-chosen string, the host a clone URL named, and
// it is bounded before it is read. That is the line the github driver's package
// comment draws in so many words, and this driver now draws it in the same
// place.
//
// THE SSH URL IS PASSED THROUGH UNCHECKED, and that is a deliberate, flagged
// asymmetry. A self-managed instance routinely serves SSH on another port or
// another name (ssh.acme.example:2222), which is a perfectly good clone address
// and a DIFFERENT repokey key — because the host is part of the identity. This
// driver reports what the instance said rather than inventing agreement; the
// consequence is that on such an instance the two addresses of one repository
// derive two keys, which is a property of the doctrine and not of this method.
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
		// THE REASON IS NOT CARRIED, because carrying it carries the URL: repokey
		// quotes what it was given, and what it was given is a string the instance
		// composed while holding the caller's credential — a clone URL with a
		// token in its identity half is the one shape repokey calls a secret at
		// rest, and this is the path that would write it into a log. Nor is the
		// reason worth much here: an instance that spells its own external URL
		// wrongly still spells a URL, so this branch is reached by an instance
		// that is broken or hostile, and for either the answer is to read what it
		// actually answered — one request, against the project named below.
		return forge.Repository{}, fmt.Errorf("gitlab: %s reported a clone URL for %s that this layer cannot derive a key from", c.host, asked)
	}
	if key.Host != c.host {
		// key.Host IS THE ONE INSTANCE-CHOSEN STRING LEFT IN THIS DRIVER'S ERRORS,
		// and it is here because it is the entire content of the mismatch: an
		// instance behind a proxy that was told the wrong external URL is a real
		// and common misconfiguration, and "which host did it claim" is the
		// answer. It is bounded — it came out of a URL already refused above the
		// length of one — lowercased by repokey, and %q-escaped.
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

// ListRepositories yields every project this credential is a member of.
//
// membership=true is the closest GitLab has to the weakest true contract: it is
// the projects this credential holds a grant on, and it OMITS public projects
// the caller merely can read. An absence therefore proves nothing, which is what
// the interface already says about every forge's listing.
//
// NOTHING IS YIELDED THAT THIS DRIVER COULD NOT THEN ACT ON. Two entries fail
// that: one whose clone URL this layer cannot derive a key from, and one whose
// key this driver's own Addressable refuses. The second was measured, not
// imagined — an entry with path_with_namespace "pay" and a clone URL at the
// instance root yielded Owner "" Name "pay", which addressable() refuses with
// "a GitLab project always lives in a namespace" — so this listing was handing
// a consumer a project every other method of this driver would refuse, under a
// key nothing could ever act on.
//
// SKIPPED RATHER THAN FAILING THE LISTING, AND THAT DISPOSITION IS THE OWNER'S
// TO REVERSE. The case for failing is that a consumer cannot tell a skipped
// entry from an absent one; the case for skipping — which is what this driver
// now does — is that forge.Lister has already settled it, in so many words: the
// contract is the weakest true one and AN ABSENCE PROVES NOTHING, so no
// consumer may read this listing as a set to begin with, and failing a whole
// listing over one project the caller never asked about denies it the rest. The
// deciding argument is neither: the github driver in this tree skips both of
// these, held by TestTheListingPagesAndSkipsWhatItCannotName, and two drivers
// over one interface may not disagree about a question the layer has written
// down. An owner who wants the other disposition wants it in BOTH drivers.
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
	if _, err := c.get(ctx, routeNamespace.at(key.Owner), nil, &namespace); err != nil {
		return forge.Repository{}, err
	}
	if namespace.ID == 0 {
		return forge.Repository{}, fmt.Errorf("gitlab: %s named no id for namespace %q", c.cfg.host, key.Owner)
	}
	if !strings.EqualFold(namespace.FullPath, key.Owner) {
		// The name it resolved TO is not quoted, for the reason repository gives:
		// it is a string the instance chose while holding the caller's
		// credential. That it did not resolve to itself is the whole fact, and
		// what it became is on the instance, one lookup away.
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
	// AND THE PROJECT THAT CAME BACK IS THE ONE THAT WAS ASKED FOR. The
	// namespace was compared before the POST, and that is a different question:
	// GitLab derives a project's PATH from what it was sent and may not keep it
	// — it has its own rules about what a path may be — so a project created
	// under the right group can still be created under the wrong name. Returning
	// it would file it under a name that does not name it, which means tau holds
	// a name it does not own and anyone may register. The github driver refuses
	// the same thing for the same reason.
	//
	// The name it became is not quoted: the instance chose that string while
	// holding the caller's credential.
	if repo.Key != key {
		return forge.Repository{}, fmt.Errorf("gitlab: %s was asked to create %q and created a project under a different path", c.cfg.host, key)
	}
	return repo, nil
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
	return c.send(ctx, routeDeleteProject.at(key.FullName()), &key, nil, nil)
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

// ListDeployKeys is the keys enabled on this project.
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

// ListPushHooks is the webhooks on this project.
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

// RemoveDeployKey names NO REPOSITORY to the taxonomy, and that is the whole of
// the difference between it and InstallDeployKey above. A 404 on
// .../deploy_keys/{id} is indistinguishable between "no such project" and "no
// such key" — GitLab answers the same status for both — and calling it
// *NotFoundError says the instance serves no repository acme/pay about a
// project that is plainly there, which a consumer acts on by dropping the
// repository. The two POSTs are on the other side deliberately: installing
// under a project that 404s IS the project being missing, because the resource
// does not exist yet to be the subject. The github driver splits the two the
// same way, held by TestA404OnAResourceIsNotAVerdictAboutTheRepository.
func (c *client) RemoveDeployKey(ctx context.Context, key repokey.Key, h forge.Handle) error {
	seg, err := handleSegment(h)
	if err != nil {
		return err
	}
	return c.send(ctx, routeRemoveDeployKey.at(key.FullName(), seg), nil, nil, nil)
}

// RegisterPushHook registers a webhook for pushes only, signed: the wire string
// is the hook's `signing_token`, from which the instance derives a Standard
// Webhooks MAC over every delivery. Never `token`, which GitLab would replay
// verbatim in a header and which binds nothing.
//
// The driver never sees the secret. It is handed the already-derived wire string
// and passes it through unaltered; re-encoding it here would register a hook
// cleanly and then reject every delivery it ever received.
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

// handleSegment refuses an empty handle rather than filling a path's hole with
// nothing. An empty last segment addresses the COLLECTION —
// DELETE /api/v4/projects/acme%2Fpay/deploy_keys/ is the whole set of them —
// so a removal of ONE thing is answered by an answer about ALL of them, with
// the caller's credential already spent, and this driver then reports nil to a
// consumer that will record the key or the hook as gone.
//
// It is refused BEFORE the request leaves the process, because a write cannot
// be undone by checking afterwards. The github driver in this tree states the
// same refusal in the same place, and two drivers over one interface may not
// disagree about a threat the layer has written down.
//
// REACHABLE WITHOUT A HOSTILE INSTANCE. InstallDeployKey above returns
// ("", err) when it takes back a key that can push, and a consumer that stored
// what it was handed and later cleaned up would arrive here with "".
//
// A HANDLE SPANNING A "/" IS REFUSED HERE TOO, and here rather than at the
// route, because a "/" inside a segment is legitimate at the route: it is how
// this driver addresses acme/team/widget. A handle is not that. It names ONE
// resource under the ONE collection its route names, so .../deploy_keys/a/b is
// not a key under .../deploy_keys, and it gets there with no dot segment in it —
// so the traversal rule the route layer holds does not reach it. The rule lives
// where the value's SHAPE is known, and that is here.
//
// THE DOT-SEGMENT REFUSAL IS HERE TOO, at the door, and again at the route. See
// addressesItself for what it is and the github driver's IsActiveMember for why
// a refusal at the route alone is too late: an operation is a sequence, and the
// last request is not the first one to spend the credential.
//
// It does not escape. Escaping is what the route does with every argument it is
// given, and a second escape here would be one this driver has to remember and
// the instance would receive doubled.
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
	// A CLONE ADDRESS IS NOT A REQUEST AND IS STILL A URL WITH A KEY IN IT.
	// repokey's grammar accepts a "?" and a "#" inside a segment — it splits on
	// "/" and refuses whitespace, and says nothing about the characters a URL
	// gives meaning to — so a project spelled pay%23evil arrives here as
	// "pay#evil" and, pasted in raw, would name the project BEFORE the "#" to
	// every clone tau ever runs with this address. So it goes through the same
	// function a request path's segments go through, over the segments a URL
	// under this host really has.
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
