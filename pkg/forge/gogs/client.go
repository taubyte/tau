package gogs

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

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
		return forge.Identity{}, errors.New("forge/gogs: the forge answered with no login, so this credential belongs to nobody")
	}
	return forge.Identity{
		Login: u.Login,
		// Subject is Gogs's user number rendered as a string and treated as
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

// Gogs returns the caller's permissions with the repository, so this is one
// request. The verdict is bound to the key asked about: permissions is nameless,
// and a transferred name may be re-registered by anyone.
func (c *client) Access(ctx context.Context, key repokey.Key) (forge.Access, error) {
	r, repo, err := c.get(ctx, key)
	if err != nil {
		return forge.AccessNone, err
	}
	if repo.Key != key {
		return forge.AccessNone, fmt.Errorf("forge/gogs: %s was asked what this credential may do to %q and answered about %q", c.d.host, key, repo.Key)
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

// /user/repos is what the credential owns and collaborates on, in one answer:
// Gogs ignores "page". One whose clone URL cannot be named or whose shape
// cannot be addressed is skipped.
func (c *client) ListRepositories(ctx context.Context, yield func(forge.Repository) bool) error {
	var out []repoJSON
	if err := c.send(ctx, routeListRepositories.at(), nil, &out); err != nil {
		return c.classify(err, repokey.Key{})
	}
	for i, r := range out {
		repo, err := c.repository(r, fmt.Sprintf("entry %d of the listing", i+1))
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
	return nil
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
		return forge.Repository{}, fmt.Errorf("forge/gogs: asked for %q and the forge created %q", key, repo.Key)
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
	type deployKeyJSON struct {
		ID       int64 `json:"id"`
		ReadOnly bool  `json:"read_only"`
	}
	var out deployKeyJSON
	body := createKeyRequest{Title: title, Key: publicKey, ReadOnly: deployKeyReadOnly}
	if err := c.send(ctx, routeInstallDeployKey.at(key.Owner, key.Name), body, &out); err != nil {
		return "", c.classify(err, key)
	}
	h, err := handle(out.ID)
	if err != nil {
		return "", err
	}
	if !out.ReadOnly {
		// Asked for read-only and given a key that can push. Whatever the
		// reason, a standing write credential tau issued is the one thing this
		// interface has no parameter for: take it back off, then say so.
		if err := c.RemoveDeployKey(ctx, key, h); err != nil {
			return "", fmt.Errorf("forge/gogs: %s installed a deploy key on %q that can push, and it could not be removed again: %w", c.d.host, key, err)
		}
		return "", fmt.Errorf("forge/gogs: %s installed a deploy key on %q that can push; it has been removed", c.d.host, key)
	}
	return h, nil
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
	var got []deployKeyJSON
	if err := c.send(ctx, routeListDeployKeys.at(key.Owner, key.Name), nil, &got); err != nil {
		return nil, c.classify(err, key)
	}
	out := make([]forge.InstalledDeployKey, 0, len(got))
	for _, k := range got {
		h, err := handle(k.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, forge.InstalledDeployKey{Handle: h, Title: k.Title, PublicKey: k.Key})
	}
	return out, nil
}

type hookJSON struct {
	ID     int64          `json:"id"`
	Config hookConfigJSON `json:"config"`
}

type hookConfigJSON struct {
	URL string `json:"url"`
}

func (c *client) ListPushHooks(ctx context.Context, key repokey.Key) ([]forge.InstalledPushHook, error) {
	var got []hookJSON
	if err := c.send(ctx, routeListPushHooks.at(key.Owner, key.Name), nil, &got); err != nil {
		return nil, c.classify(err, key)
	}
	out := make([]forge.InstalledPushHook, 0, len(got))
	for _, h := range got {
		hd, err := handle(h.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, forge.InstalledPushHook{Handle: hd, Callback: h.Config.URL})
	}
	return out, nil
}

type createHookRequest struct {
	Type   string     `json:"type"`
	Active bool       `json:"active"`
	Events []string   `json:"events"`
	Config hookConfig `json:"config"`
}

// hookConfig is Gogs's string map for a hook. It has no insecure_ssl: whether
// a Gogs verifies a callback's certificate is the instance's SKIP_TLS_VERIFY,
// not a hook's.
type hookConfig struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Secret      string `json:"secret"`
}

// RegisterPushHook subscribes to pushes and nothing else, as hook type "gogs",
// whose deliveries carry the payload and signature read here. wire is passed
// through verbatim.
func (c *client) RegisterPushHook(ctx context.Context, key repokey.Key, callback *url.URL, wire string) (forge.Handle, error) {
	var out resourceJSON
	body := createHookRequest{
		Type:   "gogs",
		Active: true,
		Events: []string{"push"},
		Config: hookConfig{
			URL:         callback.String(),
			ContentType: "json",
			Secret:      wire,
		},
	}
	if err := c.send(ctx, routeRegisterPushHook.at(key.Owner, key.Name), body, &out); err != nil {
		return "", c.classify(err, key)
	}
	return handle(out.ID)
}

// RemovePushHook asks first whether the hook is there: Gogs answers 204 to a
// DELETE for an id it has no hook under, so a wrong handle would read as
// removed. The check is a report, not a guard.
func (c *client) RemovePushHook(ctx context.Context, key repokey.Key, h forge.Handle) error {
	seg, err := handleSegment(h)
	if err != nil {
		return err
	}
	// The listing is a read UNDER the repository on behalf of a removal, so its
	// failure is classified as the removal's would be: a 404 here is not a
	// verdict that the repository is gone.
	var hooks []hookJSON
	if err := c.send(ctx, routeListPushHooks.at(key.Owner, key.Name), nil, &hooks); err != nil {
		return c.classifyResource(err)
	}
	if !slices.ContainsFunc(hooks, func(i hookJSON) bool { return strconv.FormatInt(i.ID, 10) == seg }) {
		return fmt.Errorf("forge/gogs: %s holds no hook %q on %q", c.d.host, bounded(seg), key)
	}
	if err := c.send(ctx, routeRemovePushHook.at(key.Owner, key.Name, seg), nil, nil); err != nil {
		return c.classifyResource(err)
	}
	return nil
}

// handle is the id Gogs gave a resource IT owns, as an opaque string. It is a
// string because Bitbucket's is a UUID; nothing in tau parses or orders one,
// and this is the only place the number is rendered.
func handle(id int64) (forge.Handle, error) {
	if id == 0 {
		return "", errors.New("forge/gogs: the forge returned no id for the resource it created, so nothing could ever remove it")
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
		return "", errors.New("forge/gogs: removing a resource needs the handle the forge gave it")
	}
	if strings.Contains(string(h), "/") {
		return "", fmt.Errorf("forge/gogs: the handle %q spans more than one path segment, and the route it fills has one; the removal would address a resource under a collection nobody named", bounded(string(h)))
	}
	if err := addressesItself(string(h)); err != nil {
		return "", fmt.Errorf("forge/gogs: the handle names no resource: %w", err)
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
		// "git" is the account Gogs's own image answers ssh on. An instance that set SSH_USER otherwise reports its own in
		// Repository(), which is authoritative; this is the offline guess.
		return forge.Address{URL: "ssh://git@" + c.d.host + path, SSHAccount: "git"}, nil
	}
	// Gogs authenticates an HTTPS clone by the token in the password and
	// ignores the username, so the name below is a label and not an identity.
	return forge.Address{URL: c.d.scheme + "://" + c.d.host + path, HTTPUsername: "token"}, nil
}
