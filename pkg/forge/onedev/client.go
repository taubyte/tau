package onedev

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

// listPageSize is the "count" asked for per page. OneDev refuses a count over
// its own page cap with 406 rather than clamping it, so this stays under the
// cap; and the pager stops on an EMPTY page, never a short one, so an instance
// answering fewer is still walked to the end.
const listPageSize = 100

// Whoami is who this credential belongs to, and the only honest check that it
// is still good.
func (c *client) Whoami(ctx context.Context) (forge.Identity, error) {
	var u struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := c.send(ctx, routeCurrentUser.at(), nil, &u); err != nil {
		return forge.Identity{}, c.classify(err, repokey.Key{})
	}
	if u.Name == "" || u.ID == 0 {
		return forge.Identity{}, errors.New("forge/onedev: the forge answered with no user, so this credential belongs to nobody")
	}
	// OneDev's user number, opaque from here on. OneDev does not return an
	// email address with the user, so Identity carries none.
	return forge.Identity{Login: u.Name, Subject: strconv.FormatInt(u.ID, 10)}, nil
}

// Access is the authorization question, asked the only way OneDev answers it:
// by asking for each privilege in turn and reading the first that is granted.
// See the package comment.
//
// IT IS BOUND TO THE KEY IT WAS ASKED ABOUT. The id the path resolved to is
// checked against the clone address OneDev gives that id, so a path the forge
// resolves to a project that names itself otherwise is an error here and not
// that project's grant reported as the asked one's.
func (c *client) Access(ctx context.Context, key repokey.Key) (forge.Access, error) {
	id, err := c.projectID(ctx, key)
	if err != nil {
		return forge.AccessNone, err
	}
	named, _, err := c.cloneKey(ctx, id, fmt.Sprintf("repository %q", key))
	if err != nil {
		return forge.AccessNone, err
	}
	if named != key {
		return forge.AccessNone, fmt.Errorf("forge/onedev: %s was asked what this credential may do to %q and answered about %q", c.d.host, key, named)
	}

	switch granted, err := c.granted(c.send(ctx, routeSetting.at(idSegment(id)), nil, nil)); {
	case err != nil:
		return forge.AccessNone, err
	case granted:
		return forge.AccessAdmin, nil
	}
	probe := routeReceivePack.at(key.FullName() + ".git").with(url.Values{"service": {"git-receive-pack"}})
	switch granted, err := c.granted(second(c.do(ctx, probe, nil))); {
	case err != nil:
		return forge.AccessNone, err
	case granted:
		return forge.AccessWrite, nil
	}
	_, readable, err := c.defaultBranch(ctx, id)
	switch {
	case err != nil:
		return forge.AccessNone, err
	case readable:
		return forge.AccessRead, nil
	}
	return forge.AccessNone, nil
}

// granted reads one privilege probe: 2xx is yes, 403 is OneDev's definitive
// no to an authenticated caller, and anything else is not an answer.
func (c *client) granted(err error) (bool, error) {
	switch {
	case err == nil:
		return true, nil
	case statusOf(err) == http.StatusForbidden:
		return false, nil
	}
	return false, c.classify(err, repokey.Key{})
}

func second[T any](_ T, err error) error { return err }

// Repository REPORTS, and so it does not bind: the key is derived from the
// clone address the forge returned, so a project answering for a path it no
// longer has arrives under the path it has.
func (c *client) Repository(ctx context.Context, key repokey.Key) (forge.Repository, error) {
	id, err := c.projectID(ctx, key)
	if err != nil {
		return forge.Repository{}, err
	}
	repo, ok, err := c.record(ctx, id, fmt.Sprintf("repository %q", key))
	switch {
	case err != nil:
		return forge.Repository{}, err
	case !ok:
		// The project is visible and its code is not: there is no repository
		// here this credential can read, and that is the 404 every other
		// forge gives for it.
		return forge.Repository{}, &forge.NotFoundError{Host: c.d.host, Key: key}
	}
	return repo, nil
}

type projectJSON struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

// ListRepositories walks the projects this credential can reach and yields
// those whose code it can read. OneDev lists a project to anyone who may see
// it at all, issue reporters included, and a project without code access is
// not a repository anything here can use; a root project, which a key cannot
// name, is skipped for the same reason.
func (c *client) ListRepositories(ctx context.Context, yield func(forge.Repository) bool) error {
	const maxPages = 1000
	// The offset advances by what ARRIVED, not by what was asked for: an
	// instance that answers fewer than the count would otherwise have every
	// entry between its answer and the ask skipped, silently.
	offset := 0
	for page := 0; ; page++ {
		if page >= maxPages {
			return fmt.Errorf("forge/onedev: %s is still answering a full page after %d of them; refusing to keep paging", c.d.host, maxPages)
		}
		var out []projectJSON
		q := url.Values{"query": {""}, "offset": {strconv.Itoa(offset)}, "count": {strconv.Itoa(listPageSize)}}
		if err := c.send(ctx, routeListProjects.at().with(q), nil, &out); err != nil {
			return c.classify(err, repokey.Key{})
		}
		if len(out) == 0 {
			return nil
		}
		offset += len(out)
		for i, p := range out {
			if p.ID <= 0 || !strings.Contains(p.Path, "/") {
				continue
			}
			repo, ok, err := c.record(ctx, p.ID, fmt.Sprintf("entry %d of listing page %d", i+1, page+1))
			if err != nil {
				return err
			}
			if !ok || c.d.Addressable(repo.Key) != nil {
				continue
			}
			if !yield(repo) {
				return nil
			}
		}
	}
}

type createProjectRequest struct {
	Name        string `json:"name"`
	ParentID    int64  `json:"parentId"`
	Description string `json:"description,omitempty"`
	// OneDev's create refuses a body without these two; empty objects are its
	// own defaults.
	GitPackConfig       struct{} `json:"gitPackConfig"`
	CodeIndexingSetting struct{} `json:"codeIndexingSetting"`
}

// Create makes the project AT THE KEY IT WILL BE ADDRESSED BY: under the parent
// project its owner names, which must already exist.
//
// PRIVATE IS MEASURED AND THEN HELD TO. OneDev has no visibility flag: a new
// project inherits its readers from its ancestors' grants, so whether it is
// private is decided by the tree it lands in and not by a field in this
// request. The created project is measured the way Repository measures one,
// and a project that came out other than asked is deleted again and the
// create refused — the gitea driver's rule for a writable deploy key, for the
// same reason: the caller named a property and must not be handed a resource
// without it.
func (c *client) Create(ctx context.Context, key repokey.Key, opts forge.CreateOptions) (forge.Repository, error) {
	parentID, err := c.projectID(ctx, parentOf(key))
	if err != nil {
		var nf *forge.NotFoundError
		if errors.As(err, &nf) {
			return forge.Repository{}, fmt.Errorf("forge/onedev: %s has no project %q to create %q under, or this credential may not see it", c.d.host, key.Owner, key.Name)
		}
		return forge.Repository{}, err
	}

	var id int64
	body := createProjectRequest{Name: key.Name, ParentID: parentID, Description: opts.Description}
	if err := c.send(ctx, routeCreate.at(), body, &id); err != nil {
		return forge.Repository{}, c.classify(err, repokey.Key{})
	}
	if id <= 0 {
		return forge.Repository{}, fmt.Errorf("forge/onedev: %s created %q and returned no id, so nothing could ever address it", c.d.host, key)
	}

	repo, ok, err := c.record(ctx, id, fmt.Sprintf("repository %q", key))
	if err == nil && !ok {
		err = fmt.Errorf("forge/onedev: %s created %q and this credential may not read its code", c.d.host, key)
	}
	if err == nil && repo.Key != key {
		err = fmt.Errorf("forge/onedev: asked for %q and the forge created %q", key, repo.Key)
	}
	if err == nil && repo.Private != opts.Private {
		err = fmt.Errorf("forge/onedev: %s created %q with private=%t where %t was asked; OneDev decides that from the grants the parent project passes down, not from the request", c.d.host, key, repo.Private, opts.Private)
	}
	if err != nil {
		// Made, and not what was asked for: take it back off.
		if derr := c.send(ctx, routeDelete.at(idSegment(id)), nil, nil); derr != nil {
			return forge.Repository{}, fmt.Errorf("%w; and the project it created could not be removed again: %w", err, c.classify(derr, repokey.Key{}))
		}
		return forge.Repository{}, fmt.Errorf("%w; the project has been removed", err)
	}
	return repo, nil
}

// parentOf is the key of the project key's owner names. A root parent has no
// owner of its own, which only the id lookup ever sees.
func parentOf(key repokey.Key) repokey.Key {
	parent := repokey.Key{Host: key.Host, Name: key.Owner}
	if i := strings.LastIndex(key.Owner, "/"); i >= 0 {
		parent.Owner, parent.Name = key.Owner[:i], key.Owner[i+1:]
	}
	return parent
}

// Delete is what makes AccessAdmin a real rung. OneDev refuses it with 403 to a
// credential that may not manage the project, and that stays an error.
func (c *client) Delete(ctx context.Context, key repokey.Key) error {
	id, err := c.projectID(ctx, key)
	if err != nil {
		return err
	}
	if err := c.send(ctx, routeDelete.at(idSegment(id)), nil, nil); err != nil {
		return c.classify(err, key)
	}
	return nil
}

// idSegment is the one place a project number becomes a path segment.
func idSegment(id int64) string { return strconv.FormatInt(id, 10) }

// CloneAddress is the offline derivation, with no request and no credential in
// the URL. OneDev's own clone URLs are https://host/<path>.git, and its ssh
// listens on a port of its own (6611 by default) that the key's host cannot
// carry — so the ssh spelling here is a guess on a ported host, and
// Repository(), which returns the addresses the forge gives, is authoritative.
func (c *client) CloneAddress(key repokey.Key, over forge.Protocol) (forge.Address, error) {
	escaped, err := escapedSegments(strings.Split(key.FullName(), "/"))
	if err != nil {
		return forge.Address{}, err
	}
	p := "/" + escaped + ".git"
	if over == forge.SSH {
		// OneDev authenticates ssh by the key alone and ignores the account,
		// and its own ssh clone URL names none; "git" is the conventional one
		// for clients that insist on an account.
		return forge.Address{URL: "ssh://git@" + c.d.host + p, SSHAccount: "git"}, nil
	}
	// OneDev authenticates an HTTPS clone by the access token in the password
	// and ignores the username — measured against a real one — so the name
	// below is a label and not an identity.
	return forge.Address{URL: c.d.scheme + "://" + c.d.host + p, HTTPUsername: "token"}, nil
}

// ─────────────────────────────── hooks ───────────────────────────────

// webHookJSON is the slice of a OneDev webhook this driver reads. A hook has no
// id of its own; it is an element of the project's settings, so the handle a
// hook is known by here is its callback URL, which is unique per project
// because RegisterPushHook refuses a second one.
type webHookJSON struct {
	PostURL    string          `json:"postUrl"`
	EventTypes []string        `json:"eventTypes"`
	Secret     string          `json:"secret"`
	Headers    []webHookHeader `json:"headers"`
}

type webHookHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// settings is a project's whole ProjectSetting, as OneDev sent it. Every field
// but webHooks is kept as the bytes it arrived as, because saving the settings
// REPLACES THEM ALL: a field this driver decoded and re-encoded through a
// struct that lacks it would be written back as absent.
type settings struct {
	raw   map[string]json.RawMessage
	hooks []json.RawMessage
}

func (c *client) readSettings(ctx context.Context, id int64, key repokey.Key) (settings, error) {
	var s settings
	if err := c.send(ctx, routeSetting.at(idSegment(id)), nil, &s.raw); err != nil {
		return settings{}, c.classify(err, key)
	}
	if s.raw == nil {
		return settings{}, fmt.Errorf("forge/onedev: %s answered the settings of %q with nothing", c.d.host, key)
	}
	if h, ok := s.raw["webHooks"]; ok && string(h) != "null" {
		if err := json.Unmarshal(h, &s.hooks); err != nil {
			return settings{}, fmt.Errorf("forge/onedev: %s answered webhooks for %q this driver cannot read: %w", c.d.host, key, err)
		}
	}
	return s, nil
}

// each decodes the hooks, in order.
func (s settings) each() ([]webHookJSON, error) {
	out := make([]webHookJSON, len(s.hooks))
	for i, raw := range s.hooks {
		if err := json.Unmarshal(raw, &out[i]); err != nil {
			return nil, fmt.Errorf("forge/onedev: webhook %d is not one this driver can read: %w", i+1, err)
		}
	}
	return out, nil
}

func (c *client) saveSettings(ctx context.Context, id int64, key repokey.Key, s settings) error {
	hooks, err := json.Marshal(s.hooks)
	if err != nil {
		return fmt.Errorf("forge/onedev: encoding the webhooks of %q: %w", key, err)
	}
	s.raw["webHooks"] = hooks
	if err := c.send(ctx, routeSaveSetting.at(idSegment(id)), s.raw, nil); err != nil {
		return c.classify(err, key)
	}
	return nil
}

// RegisterPushHook adds one webhook to the project's settings, subscribed to
// code pushes and NOTHING ELSE, with wire as its secret verbatim and the
// repository header Push reads.
//
// IT IS A READ-MODIFY-WRITE OF THE WHOLE SETTINGS OBJECT, because that is the
// only way OneDev's API changes a webhook. OneDev offers no conditional write,
// so an edit made to the same project's settings between this read and this
// write — by an administrator in the UI, say — is overwritten. That is a
// ceiling of the API and it is written down here rather than discovered; the
// write carries every other field exactly as it was read.
func (c *client) RegisterPushHook(ctx context.Context, key repokey.Key, callback *url.URL, wire string) (forge.Handle, error) {
	id, err := c.projectID(ctx, key)
	if err != nil {
		return "", err
	}
	named, cu, err := c.cloneKey(ctx, id, fmt.Sprintf("repository %q", key))
	if err != nil {
		return "", err
	}
	if named != key {
		return "", fmt.Errorf("forge/onedev: asked to hook %q and %s resolved it to %q", key, c.d.host, named)
	}
	s, err := c.readSettings(ctx, id, key)
	if err != nil {
		return "", err
	}
	existing, err := s.each()
	if err != nil {
		return "", err
	}
	target := callback.String()
	for _, h := range existing {
		if h.PostURL == target {
			return "", fmt.Errorf("forge/onedev: %q already has a webhook delivering to %q; a hook is known by its callback here, so a second one could never be told apart from it", key, callback.Redacted())
		}
	}
	hook, err := json.Marshal(webHookJSON{
		PostURL:    target,
		EventTypes: []string{"CODE_PUSH"},
		Secret:     wire,
		Headers:    []webHookHeader{{Name: repositoryHeader, Value: idSegment(id) + " " + cu.HTTP}},
	})
	if err != nil {
		return "", fmt.Errorf("forge/onedev: encoding the webhook for %q: %w", key, err)
	}
	s.hooks = append(s.hooks, hook)
	if err := c.saveSettings(ctx, id, key, s); err != nil {
		return "", err
	}
	return forge.Handle(target), nil
}

// RemovePushHook removes the webhook whose callback is h, and fails when the
// project has none: a removal that removed nothing is not a success.
func (c *client) RemovePushHook(ctx context.Context, key repokey.Key, h forge.Handle) error {
	if h == "" {
		return errors.New("forge/onedev: removing a webhook needs the handle it was registered under")
	}
	id, err := c.projectID(ctx, key)
	if err != nil {
		return err
	}
	s, err := c.readSettings(ctx, id, key)
	if err != nil {
		return err
	}
	hooks, err := s.each()
	if err != nil {
		return err
	}
	kept := s.hooks[:0:0]
	for i, hook := range hooks {
		if hook.PostURL != string(h) {
			kept = append(kept, s.hooks[i])
		}
	}
	if len(kept) == len(s.hooks) {
		return fmt.Errorf("forge/onedev: %q has no webhook under the handle %q", key, bounded(string(h)))
	}
	s.hooks = kept
	return c.saveSettings(ctx, id, key, s)
}

// ListPushHooks is every webhook on the project, whoever registered it, by
// callback.
func (c *client) ListPushHooks(ctx context.Context, key repokey.Key) ([]forge.InstalledPushHook, error) {
	id, err := c.projectID(ctx, key)
	if err != nil {
		return nil, err
	}
	s, err := c.readSettings(ctx, id, key)
	if err != nil {
		return nil, err
	}
	hooks, err := s.each()
	if err != nil {
		return nil, err
	}
	out := make([]forge.InstalledPushHook, 0, len(hooks))
	for _, h := range hooks {
		out = append(out, forge.InstalledPushHook{Handle: forge.Handle(h.PostURL), Callback: h.PostURL})
	}
	return out, nil
}
