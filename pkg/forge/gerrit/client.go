package gerrit

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// listPageSize is an ASK. The pager stops on an empty page or on a page whose
// last entry does not say _more_projects, never on a short one.
const listPageSize = 50

// maxPages bounds a listing the host could otherwise make endless.
const maxPages = 1000

// Whoami is who this credential belongs to, and the only honest check that it
// is still good.
func (c *client) Whoami(ctx context.Context) (forge.Identity, error) {
	var a struct {
		ID       int64  `json:"_account_id"`
		Username string `json:"username"`
		Email    string `json:"email"`
	}
	if err := c.send(ctx, routeSelf.at(), nil, &a); err != nil {
		return forge.Identity{}, c.classify(err, repokey.Key{})
	}
	if a.Username == "" {
		return forge.Identity{}, errors.New("forge/gerrit: the forge answered with no username, so this credential belongs to nobody this layer can name")
	}
	return forge.Identity{
		Login: a.Username,
		// Subject is Gerrit's account number, opaque from here on.
		Subject: strconv.FormatInt(a.ID, 10),
		Email:   a.Email,
	}, nil
}

// Access is the authorization question, BOUND TO THE KEY IT WAS ASKED ABOUT:
// the access document is nameless, so a project fetch that answered as another
// project's name makes the verdict about that other project, and it is refused.
func (c *client) Access(ctx context.Context, key repokey.Key) (forge.Access, error) {
	a, repo, err := c.get(ctx, key)
	if err != nil {
		return forge.AccessNone, err
	}
	if repo.Key != key {
		return forge.AccessNone, fmt.Errorf("forge/gerrit: %s was asked what this credential may do to %q and answered about %q", c.d.host, key, repo.Key)
	}
	return a.access(), nil
}

// Repository REPORTS, and so it does not bind: a record names itself.
func (c *client) Repository(ctx context.Context, key repokey.Key) (forge.Repository, error) {
	_, repo, err := c.get(ctx, key)
	return repo, err
}

// get is the one fetch behind Access and Repository: the project, then its
// HEAD and the caller's access to it BY THE NAME THE FORGE ANSWERED WITH, so
// every half of the record describes one project.
func (c *client) get(ctx context.Context, key repokey.Key) (accessJSON, forge.Repository, error) {
	var p projectJSON
	if err := c.send(ctx, routeProject.at(key.FullName()), nil, &p); err != nil {
		return accessJSON{}, forge.Repository{}, c.classify(err, key)
	}
	return c.describe(ctx, p.Name, key, fmt.Sprintf("project %q", key))
}

// describe completes a record for a name the forge returned. key is what the
// caller asked about, for classifying a failure; asked is what an error says.
func (c *client) describe(ctx context.Context, name string, key repokey.Key, asked string) (accessJSON, forge.Repository, error) {
	// The name is checked BEFORE it is spent on another request: a name the
	// forge chose is about to become a path this driver composes.
	if _, _, err := c.keyOf(name, asked); err != nil {
		return accessJSON{}, forge.Repository{}, err
	}
	var head string
	if err := c.send(ctx, routeProjectHEAD.at(name), nil, &head); err != nil {
		return accessJSON{}, forge.Repository{}, c.classify(err, key)
	}
	var a accessJSON
	if c.user != "" {
		if err := c.send(ctx, routeProjectAccess.at(name), nil, &a); err != nil {
			return accessJSON{}, forge.Repository{}, c.classify(err, key)
		}
	}
	repo, err := c.repository(name, head, a.restricted(), asked)
	return a, repo, err
}

// ListRepositories walks every project this credential can see. A project
// whose name has no key here is skipped rather than yielded with a key that
// may address another; a failure to describe one that does have a key is an
// error, because a listing that silently drops what it could not fetch reads
// as a complete one.
func (c *client) ListRepositories(ctx context.Context, yield func(forge.Repository) bool) error {
	for page := 0; ; page++ {
		if page == maxPages {
			return fmt.Errorf("forge/gerrit: %s is still answering after %d pages; refusing to keep paging", c.d.host, maxPages)
		}
		var got map[string]projectJSON
		q := url.Values{"n": {strconv.Itoa(listPageSize)}, "S": {strconv.Itoa(page * listPageSize)}}
		if err := c.send(ctx, routeListProjects.at().with(q), nil, &got); err != nil {
			return c.classify(err, repokey.Key{})
		}
		if len(got) == 0 {
			return nil
		}
		names := make([]string, 0, len(got))
		more := false
		for name, p := range got {
			names = append(names, name)
			more = more || p.More
		}
		slices.Sort(names)
		for i, name := range names {
			asked := fmt.Sprintf("entry %d of listing page %d", i+1, page+1)
			if _, _, err := c.keyOf(name, asked); err != nil {
				continue
			}
			_, repo, err := c.describe(ctx, name, repokey.Key{}, asked)
			if err != nil {
				return err
			}
			if !yield(repo) {
				return nil
			}
		}
		if !more {
			return nil
		}
	}
}

type createProjectRequest struct {
	Name              string   `json:"name"`
	Description       string   `json:"description,omitempty"`
	CreateEmptyCommit bool     `json:"create_empty_commit"`
	Owners            []string `json:"owners"`
}

// restrictedRead is the exclusive read rule a private project gets on each
// pattern a parent may grant read on. Gerrit has no visibility flag: a project
// is visible to whoever may read any of its refs, and an exclusive rule
// overrides only the SAME pattern inherited, not a more specific one — so
// refs/* alone leaves refs/heads/* readable to everybody All-Projects lets read
// it. Measured on 3.12.3: refs/* alone left the project at 200 for anonymous
// and for another account; these four made it 404 to both.
//
// ITS CEILING IS THE PARENT. A parent granting read on a pattern not listed
// here — refs/heads/release/* — still grants it. The upgrade is reading the
// inherited rules, which needs config access a project creator does not have.
var restrictedPatterns = []string{"refs/*", "refs/heads/*", "refs/tags/*", "refs/meta/version"}

func restrictedRead() map[string]any {
	section := map[string]any{"permissions": map[string]any{"read": map[string]any{
		"exclusive": true,
		"rules":     map[string]any{"global:Project-Owners": map[string]string{"action": "ALLOW"}},
	}}}
	add := map[string]any{}
	for _, p := range restrictedPatterns {
		add[p] = section
	}
	return map[string]any{"add": add}
}

// Create makes the project AT THE KEY IT WILL BE ADDRESSED BY, owned by the
// creator. Gerrit does not make the creator an owner by default (measured:
// is_owner absent on a project the caller just created), so the owner is named
// as the creator's single-user group, "user:<username>", which the
// singleusergroup plugin bundled with Gerrit provides.
func (c *client) Create(ctx context.Context, key repokey.Key, opts forge.CreateOptions) (forge.Repository, error) {
	if c.user == "" {
		return forge.Repository{}, errors.New("forge/gerrit: an anonymous session cannot own what it creates")
	}
	name := key.FullName()
	body := createProjectRequest{
		Name: name, Description: opts.Description, CreateEmptyCommit: true,
		Owners: []string{"user:" + c.user},
	}
	if err := c.send(ctx, routeCreateProject.at(name), body, nil); err != nil {
		return forge.Repository{}, c.classify(err, repokey.Key{})
	}
	if opts.Private {
		if err := c.send(ctx, routeRestrictProject.at(name), restrictedRead(), nil); err != nil {
			return forge.Repository{}, fmt.Errorf("forge/gerrit: %s created %q and refused to restrict who may read it, so it is as readable as its parent allows: %w", c.d.host, key, c.classify(err, repokey.Key{}))
		}
	}
	repo, err := c.Repository(ctx, key)
	if err != nil {
		return forge.Repository{}, err
	}
	if repo.Key != key {
		return forge.Repository{}, fmt.Errorf("forge/gerrit: asked for %q and the forge created %q", key, repo.Key)
	}
	return repo, nil
}

// Delete is the delete-project plugin's DELETE /projects/{name}, which needs
// the Delete Project capability, Delete Own Projects on a project the caller
// owns, or Administrators.
func (c *client) Delete(ctx context.Context, key repokey.Key) error {
	if err := c.send(ctx, routeDeleteProject.at(key.FullName()), nil, nil); err != nil {
		return c.classify(err, key)
	}
	return nil
}

// CloneAddress is derived from the key with no request and no credential in
// the URL. Gerrit serves git over HTTP at /{project} and authenticates with the
// same username and HTTP password the API takes, so the username is a real
// identity here and not a label.
func (c *client) CloneAddress(key repokey.Key, over forge.Protocol) (forge.Address, error) {
	escaped, err := projectPath(key.FullName())
	if err != nil {
		return forge.Address{}, err
	}
	if over == forge.SSH {
		if c.user == "" {
			return forge.Address{}, errors.New("forge/gerrit: Gerrit's sshd authenticates accounts, and an anonymous session has none")
		}
		return forge.Address{URL: c.sshURL(escaped), SSHAccount: c.user}, nil
	}
	return forge.Address{URL: c.d.scheme + "://" + c.d.host + "/" + escaped, HTTPUsername: c.user}, nil
}
