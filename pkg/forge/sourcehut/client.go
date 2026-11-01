package sourcehut

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

var (
	docMe = document{"Me", `query Me { me { id username canonicalName email } }`}

	docRepository = document{"Repository", `query Repository($owner: String!, $name: String!) {
  user(username: $owner) { repository(name: $name) { ` + repoFields + ` } }
}`}

	// docAccess is docRepository asked together with who is asking, so the
	// ownership that makes a grant admin is read off the same answer.
	docAccess = document{"Access", `query Access($owner: String!, $name: String!) {
  me { canonicalName }
  user(username: $owner) { repository(name: $name) { ` + repoFields + ` } }
}`}

	docList = document{"List", `query List($cursor: Cursor) {
  me { repositories(cursor: $cursor) { results { ` + repoFields + ` } cursor } }
}`}

	docCreate = document{"Create", `mutation Create($name: String!, $visibility: Visibility!, $description: String) {
  createRepository(name: $name, visibility: $visibility, description: $description) { ` + repoFields + ` }
}`}

	docDelete = document{"Delete", `mutation Delete($id: Int!) { deleteRepository(id: $id) { ` + repoFields + ` } }`}

	docInstallKey = document{"InstallKey", `mutation InstallKey($repo: ID!, $key: String!) {
  createDeployKey(repo: $repo, mode: RO, key: $key) { rid access repo { name owner { canonicalName } } }
}`}

	docKeys = document{"Keys", `query Keys($owner: String!, $name: String!, $cursor: Cursor) {
  user(username: $owner) { repository(name: $name) { name owner { canonicalName }
    deployKeys(cursor: $cursor) { results { rid key comment } cursor } } }
}`}

	docRemoveKey = document{"RemoveKey", `mutation RemoveKey($rid: ID!) { deleteDeployKey(rid: $rid) { rid } }`}

	docRegisterHook = document{"RegisterHook", `mutation RegisterHook($config: GitWebhookInput!) {
  createGitWebhook(config: $config) { id url }
}`}

	docHooks = document{"Hooks", `query Hooks($id: Int!, $cursor: Cursor) {
  gitWebhooks(repositoryID: $id, cursor: $cursor) { results { id url } cursor }
}`}

	docRemoveHook = document{"RemoveHook", `mutation RemoveHook($id: Int!) { deleteGitWebhook(id: $id) { id } }`}
)

// maxPages bounds a listing whose forge keeps answering a cursor.
const maxPages = 1000

func (c *client) Whoami(ctx context.Context) (forge.Identity, error) {
	var out struct {
		Me *userJSON `json:"me"`
	}
	if err := c.send(ctx, docMe, nil, &out); err != nil {
		return forge.Identity{}, c.classify(err)
	}
	if out.Me == nil || out.Me.Username == "" {
		return forge.Identity{}, errors.New("forge/sourcehut: the forge answered with no user, so this credential belongs to nobody")
	}
	return forge.Identity{
		Login:   out.Me.Username,
		Subject: strconv.FormatInt(out.Me.ID, 10),
		Email:   out.Me.Email,
	}, nil
}

type userJSON struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	CanonicalName string `json:"canonicalName"`
	Email         string `json:"email"`
}

// lookup resolves a key to the repository the forge answers with, and to the
// caller's own canonical name. A repository the credential cannot see is null
// on sourcehut exactly as a missing one is, so both are NotFoundError.
func (c *client) lookup(ctx context.Context, key repokey.Key, doc document) (repoJSON, forge.Repository, string, error) {
	var out struct {
		Me   *userJSON `json:"me"`
		User *struct {
			Repository *repoJSON `json:"repository"`
		} `json:"user"`
	}
	vars := map[string]any{"owner": strings.TrimPrefix(key.Owner, "~"), "name": key.Name}
	if err := c.send(ctx, doc, vars, &out); err != nil {
		return repoJSON{}, forge.Repository{}, "", c.classify(err)
	}
	if out.User == nil || out.User.Repository == nil {
		return repoJSON{}, forge.Repository{}, "", &forge.NotFoundError{Host: c.d.host, Key: key}
	}
	repo, err := c.repository(*out.User.Repository, fmt.Sprintf("repository %q", key))
	if err != nil {
		return repoJSON{}, forge.Repository{}, "", err
	}
	me := ""
	if out.Me != nil {
		me = out.Me.CanonicalName
	}
	return *out.User.Repository, repo, me, nil
}

// resolve is lookup for a write: the answer must describe the key asked about,
// because the id it yields is what the write is aimed at.
func (c *client) resolve(ctx context.Context, key repokey.Key) (repoJSON, error) {
	r, repo, _, err := c.lookup(ctx, key, docRepository)
	if err != nil {
		return repoJSON{}, err
	}
	if repo.Key != key {
		return repoJSON{}, fmt.Errorf("forge/sourcehut: %s was asked about %q and answered about %q; a write aimed by that answer would land on a repository nobody named", c.d.host, key, repo.Key)
	}
	return r, nil
}

// Access is what this credential may do. sourcehut answers RO or RW; the
// repository's OWNER is the one account that may administer it — hooks, deploy
// keys, deletion — so ownership is admin.
func (c *client) Access(ctx context.Context, key repokey.Key) (forge.Access, error) {
	r, repo, me, err := c.lookup(ctx, key, docAccess)
	if err != nil {
		return forge.AccessNone, err
	}
	if repo.Key != key {
		return forge.AccessNone, fmt.Errorf("forge/sourcehut: %s was asked what this credential may do to %q and answered about %q", c.d.host, key, repo.Key)
	}
	switch {
	case me != "" && strings.EqualFold(me, r.Owner.CanonicalName):
		return forge.AccessAdmin, nil
	case r.Access == "RW":
		return forge.AccessWrite, nil
	case r.Access == "RO":
		return forge.AccessRead, nil
	}
	return forge.AccessNone, nil
}

func (c *client) Repository(ctx context.Context, key repokey.Key) (forge.Repository, error) {
	_, repo, _, err := c.lookup(ctx, key, docRepository)
	if err != nil {
		return forge.Repository{}, err
	}
	return repo, nil
}

// ListRepositories yields this credential's own repositories: sourcehut's
// listing is of the caller's account, and there is no organization to widen it.
func (c *client) ListRepositories(ctx context.Context, yield func(forge.Repository) bool) error {
	stopped := false
	err := c.pages(func(cursor *string) (*string, error) {
		var out struct {
			Me *struct {
				Repositories struct {
					Results []repoJSON `json:"results"`
					Cursor  *string    `json:"cursor"`
				} `json:"repositories"`
			} `json:"me"`
		}
		if err := c.send(ctx, docList, map[string]any{"cursor": cursor}, &out); err != nil {
			return nil, c.classify(err)
		}
		if out.Me == nil {
			return nil, errors.New("forge/sourcehut: the listing answered no user")
		}
		for i, r := range out.Me.Repositories.Results {
			repo, err := c.repository(r, fmt.Sprintf("entry %d of a listing", i+1))
			if err != nil || c.d.Addressable(repo.Key) != nil {
				continue
			}
			if !yield(repo) {
				stopped = true
				return nil, nil
			}
		}
		return out.Me.Repositories.Cursor, nil
	})
	if stopped {
		return nil
	}
	return err
}

// pages walks a cursor until the forge answers none. A cursor the forge hands
// back twice is a loop and not a listing.
func (c *client) pages(fetch func(cursor *string) (*string, error)) error {
	var cursor *string
	seen := map[string]bool{}
	for page := 1; ; page++ {
		if page > maxPages {
			return fmt.Errorf("forge/sourcehut: %s is still answering a cursor after %d pages; refusing to keep paging", c.d.host, maxPages)
		}
		next, err := fetch(cursor)
		if err != nil {
			return err
		}
		if next == nil || *next == "" {
			return nil
		}
		if seen[*next] {
			return fmt.Errorf("forge/sourcehut: %s answered a cursor it had already answered; refusing to page in a loop", c.d.host)
		}
		seen[*next] = true
		cursor = next
	}
}

// Create makes key under the credential's own account, the only place
// sourcehut creates one.
func (c *client) Create(ctx context.Context, key repokey.Key, opts forge.CreateOptions) (forge.Repository, error) {
	me, err := c.Whoami(ctx)
	if err != nil {
		return forge.Repository{}, err
	}
	if !strings.EqualFold(key.Owner, "~"+me.Login) {
		return forge.Repository{}, fmt.Errorf("forge/sourcehut: %q is not this credential's account; sourcehut creates a repository only under ~%s", key.Owner, me.Login)
	}
	visibility := "PUBLIC"
	if opts.Private {
		visibility = "PRIVATE"
	}
	vars := map[string]any{"name": key.Name, "visibility": visibility}
	if opts.Description != "" {
		vars["description"] = opts.Description
	}
	var out struct {
		Created *repoJSON `json:"createRepository"`
	}
	if err := c.send(ctx, docCreate, vars, &out); err != nil {
		return forge.Repository{}, c.classify(err)
	}
	if out.Created == nil {
		return forge.Repository{}, fmt.Errorf("forge/sourcehut: %s answered the creation of %q with no repository", c.d.host, key)
	}
	repo, err := c.repository(*out.Created, fmt.Sprintf("repository %q", key))
	if err != nil {
		return forge.Repository{}, err
	}
	if repo.Key != key {
		return forge.Repository{}, fmt.Errorf("forge/sourcehut: asked for %q and the forge created %q", key, repo.Key)
	}
	return repo, nil
}

func (c *client) Delete(ctx context.Context, key repokey.Key) error {
	r, err := c.resolve(ctx, key)
	if err != nil {
		return err
	}
	var out struct {
		Deleted *repoJSON `json:"deleteRepository"`
	}
	if err := c.send(ctx, docDelete, map[string]any{"id": r.ID}, &out); err != nil {
		return c.classify(err)
	}
	if out.Deleted == nil {
		return &forge.NotFoundError{Host: c.d.host, Key: key}
	}
	gone, err := c.repository(*out.Deleted, fmt.Sprintf("the deletion of %q", key))
	if err != nil {
		return err
	}
	if gone.Key != key {
		return fmt.Errorf("forge/sourcehut: asked to delete %q and the forge deleted %q", key, gone.Key)
	}
	return nil
}

// InstallDeployKey installs a READ-ONLY key. The title is the key's comment,
// which is the one label sourcehut keeps.
func (c *client) InstallDeployKey(ctx context.Context, key repokey.Key, title, publicKey string) (forge.Handle, error) {
	fields := strings.Fields(publicKey)
	if len(fields) < 2 {
		return "", errors.New("forge/sourcehut: a deploy key is an OpenSSH public key, a type and a blob")
	}
	if title == "" || len(strings.Fields(title)) != 1 || title != strings.TrimSpace(title) {
		return "", errors.New("forge/sourcehut: a deploy key's title is its comment on sourcehut, so it is one word")
	}
	r, err := c.resolve(ctx, key)
	if err != nil {
		return "", err
	}
	var out struct {
		Key *struct {
			RID    string `json:"rid"`
			Access string `json:"access"`
			Repo   struct {
				Name  string `json:"name"`
				Owner struct {
					CanonicalName string `json:"canonicalName"`
				} `json:"owner"`
			} `json:"repo"`
		} `json:"createDeployKey"`
	}
	vars := map[string]any{"repo": r.RID, "key": fields[0] + " " + fields[1] + " " + title}
	if err := c.send(ctx, docInstallKey, vars, &out); err != nil {
		return "", c.classify(err)
	}
	if out.Key == nil || out.Key.RID == "" {
		return "", errors.New("forge/sourcehut: the forge returned no id for the deploy key it created, so nothing could ever remove it")
	}
	h := forge.Handle(out.Key.RID)
	on, err := c.keyOf(out.Key.Repo.Owner.CanonicalName, out.Key.Repo.Name, "the deploy key's repository")
	switch {
	case err != nil || on != key:
		return "", c.undo(ctx, key, h, "on a repository other than the one asked about")
	case out.Key.Access != "RO":
		return "", c.undo(ctx, key, h, "that can push")
	}
	return h, nil
}

func (c *client) undo(ctx context.Context, key repokey.Key, h forge.Handle, why string) error {
	var out struct {
		Removed *struct {
			RID string `json:"rid"`
		} `json:"deleteDeployKey"`
	}
	if err := c.send(ctx, docRemoveKey, map[string]any{"rid": string(h)}, &out); err != nil {
		return fmt.Errorf("forge/sourcehut: %s installed a deploy key for %q %s, and it could not be removed again: %w", c.d.host, key, why, c.classify(err))
	}
	return fmt.Errorf("forge/sourcehut: %s installed a deploy key for %q %s; it has been removed", c.d.host, key, why)
}

type deployKeyJSON struct {
	RID     string `json:"rid"`
	Key     string `json:"key"`
	Comment string `json:"comment"`
}

func (c *client) ListDeployKeys(ctx context.Context, key repokey.Key) ([]forge.InstalledDeployKey, error) {
	var out []forge.InstalledDeployKey
	err := c.pages(func(cursor *string) (*string, error) {
		var got struct {
			User *struct {
				Repository *struct {
					Name  string `json:"name"`
					Owner struct {
						CanonicalName string `json:"canonicalName"`
					} `json:"owner"`
					DeployKeys struct {
						Results []deployKeyJSON `json:"results"`
						Cursor  *string         `json:"cursor"`
					} `json:"deployKeys"`
				} `json:"repository"`
			} `json:"user"`
		}
		vars := map[string]any{"owner": strings.TrimPrefix(key.Owner, "~"), "name": key.Name, "cursor": cursor}
		if err := c.send(ctx, docKeys, vars, &got); err != nil {
			return nil, c.classify(err)
		}
		if got.User == nil || got.User.Repository == nil {
			return nil, &forge.NotFoundError{Host: c.d.host, Key: key}
		}
		repo := got.User.Repository
		on, err := c.keyOf(repo.Owner.CanonicalName, repo.Name, fmt.Sprintf("the deploy keys of %q", key))
		if err != nil {
			return nil, err
		}
		if on != key {
			return nil, fmt.Errorf("forge/sourcehut: %s was asked for the deploy keys of %q and answered with those of %q", c.d.host, key, on)
		}
		for _, k := range repo.DeployKeys.Results {
			if k.RID == "" {
				return nil, errors.New("forge/sourcehut: the forge listed a deploy key with no id")
			}
			out = append(out, forge.InstalledDeployKey{Handle: forge.Handle(k.RID), Title: k.Comment, PublicKey: k.Key})
		}
		return repo.DeployKeys.Cursor, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveDeployKey removes the key h names FROM key. sourcehut deletes a deploy
// key by a global id, so the key's own listing is what proves h is one of its.
func (c *client) RemoveDeployKey(ctx context.Context, key repokey.Key, h forge.Handle) error {
	if err := handleOK(h); err != nil {
		return err
	}
	keys, err := c.ListDeployKeys(ctx, key)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(keys, func(k forge.InstalledDeployKey) bool { return k.Handle == h }) {
		return fmt.Errorf("forge/sourcehut: %q has no deploy key %q", key, bounded(string(h)))
	}
	var out struct {
		Removed *struct {
			RID string `json:"rid"`
		} `json:"deleteDeployKey"`
	}
	if err := c.send(ctx, docRemoveKey, map[string]any{"rid": string(h)}, &out); err != nil {
		return c.classify(err)
	}
	if out.Removed == nil || out.Removed.RID != string(h) {
		return fmt.Errorf("forge/sourcehut: %s did not say it removed deploy key %q", c.d.host, bounded(string(h)))
	}
	return nil
}

// RegisterPushHook subscribes callback to post-receive events on key, with a
// query whose answer carries wire — the hook's binding (see
// forge.SchemePayloadEd25519).
func (c *client) RegisterPushHook(ctx context.Context, key repokey.Key, callback *url.URL, wire string) (forge.Handle, error) {
	query, err := hookQuery(wire)
	if err != nil {
		return "", err
	}
	r, err := c.resolve(ctx, key)
	if err != nil {
		return "", err
	}
	var out struct {
		Hook *hookJSON `json:"createGitWebhook"`
	}
	config := map[string]any{
		"repositoryID": r.ID,
		"url":          callback.String(),
		"events":       []string{"GIT_POST_RECEIVE"},
		"query":        query,
	}
	if err := c.send(ctx, docRegisterHook, map[string]any{"config": config}, &out); err != nil {
		return "", c.classify(err)
	}
	if out.Hook == nil || out.Hook.ID == 0 {
		return "", errors.New("forge/sourcehut: the forge returned no id for the webhook it created, so nothing could ever remove it")
	}
	return forge.Handle(strconv.FormatInt(out.Hook.ID, 10)), nil
}

type hookJSON struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
}

func (c *client) ListPushHooks(ctx context.Context, key repokey.Key) ([]forge.InstalledPushHook, error) {
	r, err := c.resolve(ctx, key)
	if err != nil {
		return nil, err
	}
	var out []forge.InstalledPushHook
	err = c.pages(func(cursor *string) (*string, error) {
		var got struct {
			Hooks struct {
				Results []hookJSON `json:"results"`
				Cursor  *string    `json:"cursor"`
			} `json:"gitWebhooks"`
		}
		if err := c.send(ctx, docHooks, map[string]any{"id": r.ID, "cursor": cursor}, &got); err != nil {
			return nil, c.classify(err)
		}
		for _, h := range got.Hooks.Results {
			if h.ID == 0 {
				return nil, errors.New("forge/sourcehut: the forge listed a webhook with no id")
			}
			out = append(out, forge.InstalledPushHook{Handle: forge.Handle(strconv.FormatInt(h.ID, 10)), Callback: h.URL})
		}
		return got.Hooks.Cursor, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RemovePushHook removes the hook h names FROM key, after key's own listing
// proved h is one of its: sourcehut deletes a webhook by a global id.
func (c *client) RemovePushHook(ctx context.Context, key repokey.Key, h forge.Handle) error {
	if err := handleOK(h); err != nil {
		return err
	}
	id, err := strconv.ParseInt(string(h), 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("forge/sourcehut: %q is not a webhook id; sourcehut numbers its webhooks", bounded(string(h)))
	}
	hooks, err := c.ListPushHooks(ctx, key)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(hooks, func(k forge.InstalledPushHook) bool { return k.Handle == h }) {
		return fmt.Errorf("forge/sourcehut: %q has no webhook %q", key, bounded(string(h)))
	}
	var out struct {
		Removed *hookJSON `json:"deleteGitWebhook"`
	}
	if err := c.send(ctx, docRemoveHook, map[string]any{"id": id}, &out); err != nil {
		return c.classify(err)
	}
	if out.Removed == nil || out.Removed.ID != id {
		return fmt.Errorf("forge/sourcehut: %s did not say it removed webhook %q", c.d.host, bounded(string(h)))
	}
	return nil
}

// handleOK refuses a handle before the wire when it names nothing.
func handleOK(h forge.Handle) error {
	switch {
	case h == "":
		return errors.New("forge/sourcehut: removing a resource needs the handle the forge gave it")
	case len(h) > maxHostString:
		return errors.New("forge/sourcehut: the handle is longer than any id the forge hands out")
	}
	return nil
}

func (c *client) CloneAddress(key repokey.Key, over forge.Protocol) (forge.Address, error) {
	addr, ok := c.addresses(key)[over]
	if !ok {
		return forge.Address{}, fmt.Errorf("forge/sourcehut: no clone address over %s", over)
	}
	return addr, nil
}
