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

// listPageSize is GitHub's maximum. Pagination here is a page counter that
// stops on a short page rather than a walk of the Link header: the header's
// next URL is a string the host chooses, and following one is following a host
// wherever it points. The pin would refuse another host, but a page counter
// cannot be pointed anywhere at all.
const listPageSize = 100

// deployKeyReadOnly is written true at the one place it exists.
//
// The interface has no parameter for it and this constant is not configurable.
// tau's deploy key exists so a builder can CLONE; nothing under services/ pushes
// with one. v1 installs a write key on every repository it registers — not by
// decision but because it sets a title and a key and its SDK's zero value for
// read_only is false — and removes none, so every repository it has ever
// registered carries a standing write credential it issued and forgot.
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

// Access is the authorization question, answered from the repository fetch
// itself: GitHub returns the caller's own permissions with the repository, so
// this is one request and not two.
//
// A definitive answer is a value, INCLUDING (AccessNone, nil). Everything that
// is not an answer is an error — see classify — so an outage, a rate limit or a
// blocked OAuth app can never arrive at a consumer as a denial.
//
// AND IT IS BOUND TO THE KEY IT WAS ASKED ABOUT. `permissions` is a nameless
// field: it describes whatever repository the body describes, so reading it as
// the answer for a different key answers a question nobody asked. GitHub
// answers a renamed or TRANSFERRED repository by naming the one it became — and
// a transferred name is free for anyone to re-register the moment it is
// transferred, so "admin on whatever victim/pay currently points at" is a grant
// on a name an attacker may already hold. The same fetch already carries
// clone_url and get already derives a key from it; all that was missing was
// asking whether that key is the one this call was about.
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

// Repository REPORTS, and so it does not bind. A forge.Repository names itself:
// the key is derived from the clone address the forge returned, so a rename
// arrives as a different key rather than as an error, and a consumer that reads
// it sees the move the forge is reporting. That asymmetry with Access is the
// point — a value that names itself may differ from the question; a VERDICT may
// not, because the verdict carries no name and would be filed under the
// question's.
func (c *client) Repository(ctx context.Context, key repokey.Key) (forge.Repository, error) {
	_, repo, err := c.get(ctx, key)
	if err != nil {
		return forge.Repository{}, err
	}
	return repo, nil
}

// get is the one fetch behind both Access and Repository. It returns the body
// AND the repository derived from it, so the key the forge named is computed
// once, by the same code, whichever of the two asked — and the two then differ
// only in what they do about a disagreement, which is a decision each states
// for itself rather than a second derivation that could drift from this one.
//
// Case is not a disagreement: both keys come from repokey, which lowercases.
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

// ListRepositories walks the repositories this credential can reach.
//
// IT PASSES NO AFFILIATION. GitHub's affiliation=owner,collaborator,
// organization_member is GitHub's vocabulary and means nothing on another
// forge, so the interface has no place to carry it and this driver takes
// GitHub's own default. A repository whose clone URL this layer cannot name, or
// whose shape this driver cannot address, is skipped rather than yielded with a
// key nothing could act on — the alternative is failing a whole listing over
// one repository the caller never asked about.
func (c *client) ListRepositories(ctx context.Context, yield func(forge.Repository) bool) error {
	// A FORGE THAT ALWAYS ANSWERS A FULL PAGE IS AN UNBOUNDED LOOP inside a call
	// a consumer thinks is one request. Stopping on a short page is termination
	// while the forge is honest, which is not the same thing as termination: the
	// gitlab driver bounds its own pager for this reason and this one did not,
	// and the two drivers may not disagree about a threat the layer has written
	// down. A hundred thousand repositories is more than any credential reaches
	// and the same ceiling the other driver keeps.
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

// Create makes the repository AT THE KEY IT WILL BE ADDRESSED BY, namespace
// included. v1 cannot express that — both of its create paths pass an empty org
// and can therefore only create under the authenticated account, even where its
// tenancy names one — so the owner is read here and the endpoint chosen from it.
//
// The extra Whoami is what buys that: POST /orgs/{me}/repos is a 404 because a
// user is not an organization, so which endpoint applies genuinely depends on
// who the credential is.
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
	// The derived key is checked against the argument, the same way get() checks
	// it for a read. Here it is a repository MADE somewhere nobody asked for, and
	// returning it would file it under a name that does not name it — which on a
	// forge that silently created it elsewhere means tau holds a name it does not
	// own and anyone may register.
	if repo.Key != key {
		return forge.Repository{}, fmt.Errorf("forge/github: asked for %q and the forge created %q", key, repo.Key)
	}
	return repo, nil
}

// Delete is what makes AccessAdmin a real rung rather than a speculative one.
// v1's CLI discovers it lacked admin by string-matching an error body.
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

// RegisterPushHook subscribes to pushes and NOTHING ELSE. The events list is
// one element because every additional event is a delivery this deployment then
// has to have an opinion about, and insecure_ssl is "0" because the callback is
// where a secret-bearing delivery is sent.
//
// wire is passed through verbatim. It is the string forge.Scheme.Wire derived
// from a Secret this driver has never seen and cannot reach, and re-encoding it
// here would register a hook that looks perfect and rejects every delivery it
// ever receives.
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

// handleSegment says what a handle IS: the name of ONE resource under the ONE
// collection its route names, and therefore ONE path segment.
//
// An empty handle addresses the COLLECTION and would answer a removal of one
// thing with an answer about all of them. A handle spanning a "/" addresses a
// resource under a collection nobody named — .../keys/a/b is not a key under
// .../keys — and it does that with no dot segment in it, so the traversal rule
// the route layer holds does not reach it. The route layer cannot hold this one
// either: a "/" inside a segment is legitimate there, which is the whole of how
// GitLab addresses a project, so the rule lives where the value's SHAPE is
// known and that is here.
//
// THE DOT-SEGMENT REFUSAL IS HERE TOO, at the door, and again at the route. See
// addressesItself for what it is and IsActiveMember for why a refusal at the
// route alone is too late: an operation is a sequence, and the last request is
// not the first one to spend the credential.
//
// It does not escape. Escaping is what the route does with every argument it is
// given, and a second escape here would be one this driver has to remember and
// GitHub would receive doubled.
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

// CloneAddress is the address a clone uses and the account it presents as, both
// derived from the key with no request and no credential in the URL.
//
// It retires two things from pkg/git at once: the literal "github.com" it
// string-replaces to build a remote, and the token it splices into that
// remote — which then lands in .git/config on disk, in process arguments, and
// in every error quoting the remote. What crosses here are facts; the token
// belongs in the transport.
//
// A HOST CARRYING A PORT CAN ONLY HAVE ONE OF THESE TWO RIGHT, and the key
// cannot say which. repokey has one host string, and the port in it came from
// whichever transport the clone URL that named the repository used — so on
// https://ghes.example:8443/... the ssh spelling below carries 8443, which is
// an https port. Repository() returns the addresses the FORGE gives, which are
// authoritative; this method is the offline derivation, and a caller that needs
// certainty on a ported host should ask the forge.
func (c *client) CloneAddress(key repokey.Key, over forge.Protocol) (forge.Address, error) {
	// A CLONE ADDRESS IS NOT A REQUEST AND IS STILL A URL WITH A KEY IN IT.
	// repokey's grammar accepts a "?" and a "#" inside a segment — it splits on
	// "/" and refuses whitespace, and says nothing about the characters a URL
	// gives meaning to — so a name spelled pay%23evil arrives here as "pay#evil"
	// and, pasted in raw, would name the repository BEFORE the "#" to every
	// clone tau ever runs with this address. So it goes through the same
	// function a request path's segments go through.
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
