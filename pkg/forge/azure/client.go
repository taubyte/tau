package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const (
	maxResponseBody = 8 << 20
	maxHostString   = 256
	maxDrainOnError = 4 << 10
)

// gitNamespace is the Git Repositories security namespace, and the bits below
// are its permissions. They are Azure DevOps constants, the same on every
// Services organization and Server collection.
const (
	gitNamespace = "2e9eb7ed-3c0a-47d4-87c1-0ffdd275fd87"

	permRead              = 2    // GenericRead
	permContribute        = 4    // GenericContribute: push
	permManagePermissions = 8192 // ManagePermissions: the repository's admin
)

// client is a driver acting as one credential. It holds no copy of the
// credential: the closure writing the header is the only place it lives.
type client struct {
	d    *driver
	http *http.Client
}

var (
	_ forge.Client         = (*client)(nil)
	_ forge.Lister         = (*client)(nil)
	_ forge.Creator        = (*client)(nil)
	_ forge.Deleter        = (*client)(nil)
	_ forge.CloneAddresses = (*client)(nil)
)

func (d *driver) bind(token string) *client {
	return &client{
		d: d,
		http: &http.Client{
			CheckRedirect: noRedirect,
			Transport: forge.PinnedTransport(d.host, d.rt, func(h http.Header) {
				// An anonymous session sends nothing: a public project is
				// readable without a credential, and an empty one is a 401.
				if token != "" {
					h.Set("Authorization", d.authorization(token))
				}
			}),
		},
	}
}

// noRedirect: THIS DRIVER FOLLOWS NO REDIRECT. Azure DevOps answers a request
// it will not authenticate with a 302 to its sign-in page, and a write
// replayed by a 307 lands wherever the answer chose. Either way the 3xx is
// handed to send, which reports it as the non-2xx it is.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (c *client) Host() string { return c.d.host }

type statusError struct {
	Method string
	Path   string
	Status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("forge/azure: %s %s answered %d %s", e.Method, e.Path, e.Status, http.StatusText(e.Status))
}

func statusOf(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

// send issues one request and decodes a 2xx body into out.
//
// 203 IS NOT SUCCESS HERE. Azure DevOps answers a credential it does not
// accept with 203 Non-Authoritative Information and its HTML sign-in page; a
// driver that took every 2xx as an answer would try to decode that page, or —
// with no body expected — report a deletion that never happened.
func (c *client) send(ctx context.Context, rq request, in, out any) error {
	p, err := rq.path()
	if err != nil {
		return err
	}
	method := rq.route.method
	query, err := rq.rawQuery()
	if err != nil {
		return err
	}
	u := c.d.base.String() + p + "?" + query

	var body io.Reader
	if in != nil {
		enc, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("forge/azure: encoding the %s %s request: %w", method, p, err)
		}
		body = bytes.NewReader(enc)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return fmt.Errorf("forge/azure: building %s %s: %w", method, p, err)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("forge/azure: %s %s: %w", method, p, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 || resp.StatusCode == http.StatusNonAuthoritativeInfo {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainOnError))
		return &statusError{Method: method, Path: p, Status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		return fmt.Errorf("forge/azure: %s %s answered a body this driver cannot read: %w", method, p, err)
	}
	return nil
}

// classify maps a failure about one repository into the taxonomy: 401 and 203
// are the credential, 404 is "no such repository" (which on Azure DevOps also
// covers one this credential may not see). Everything else — a 403 in
// particular — stays an error and is not a verdict.
func (c *client) classify(err error, key repokey.Key) error {
	switch s := statusOf(err); {
	case s == http.StatusUnauthorized || s == http.StatusNonAuthoritativeInfo:
		return &forge.CredentialError{Host: c.d.host, Reason: "the forge refused this credential"}
	case s == http.StatusNotFound && key.Name != "":
		return &forge.NotFoundError{Host: c.d.host, Key: key}
	}
	return err
}

// ─────────────────────────────── identity ───────────────────────────────

type connectionData struct {
	AuthenticatedUser struct {
		ID         string `json:"id"`
		Properties struct {
			Account struct {
				Value string `json:"$value"`
			} `json:"Account"`
		} `json:"properties"`
	} `json:"authenticatedUser"`
}

// Whoami asks the first organization this deployment answers in. Login is the
// account name Azure DevOps keeps (a UPN or e-mail on Services, DOMAIN\user on
// a Server); Subject is the identity's id, opaque from here on.
func (c *client) Whoami(ctx context.Context) (forge.Identity, error) {
	var cd connectionData
	if err := c.send(ctx, routeConnectionData.at(c.d.orgs[0]), nil, &cd); err != nil {
		return forge.Identity{}, c.classify(err, repokey.Key{})
	}
	u := cd.AuthenticatedUser
	if u.ID == "" || u.Properties.Account.Value == "" {
		return forge.Identity{}, errors.New("forge/azure: the forge answered with no account, so this credential belongs to nobody")
	}
	id := forge.Identity{Login: u.Properties.Account.Value, Subject: u.ID}
	if strings.Contains(id.Login, "@") {
		id.Email = id.Login
	}
	return id, nil
}

// ─────────────────────────────── repositories ───────────────────────────────

type repoJSON struct {
	ID            string `json:"id"`
	RemoteURL     string `json:"remoteUrl"`
	SSHURL        string `json:"sshUrl"`
	DefaultBranch string `json:"defaultBranch"`
	IsDisabled    bool   `json:"isDisabled"`
	Project       struct {
		ID         string `json:"id"`
		Visibility string `json:"visibility"`
	} `json:"project"`
}

// repository derives the key from the remote URL the forge returned, refuses
// one on another host or of a shape this driver cannot address, and never
// quotes the URL: a host-chosen string may carry an identity half.
func (c *client) repository(r repoJSON, asked string) (forge.Repository, error) {
	switch {
	case r.RemoteURL == "":
		return forge.Repository{}, fmt.Errorf("forge/azure: %s described %s with no remote URL, so there is nothing to derive a key from", c.d.host, asked)
	case len(r.RemoteURL) > maxHostString:
		return forge.Repository{}, fmt.Errorf("forge/azure: %s described %s with a %d-byte remote URL, which is not one", c.d.host, asked, len(r.RemoteURL))
	case r.ID == "" || r.Project.ID == "":
		return forge.Repository{}, fmt.Errorf("forge/azure: %s described %s without the ids every write to it is addressed by", c.d.host, asked)
	}
	u, err := url.Parse(r.RemoteURL)
	if err != nil {
		return forge.Repository{}, fmt.Errorf("forge/azure: %s reported a remote URL for %s that is not a URL", c.d.host, asked)
	}
	// Azure DevOps puts the organization in the user half
	// (https://acme@dev.azure.com/...). It is a label, not a credential, and
	// not part of the address.
	u.User = nil
	key, err := repokey.Parse(u.String())
	if err != nil {
		return forge.Repository{}, fmt.Errorf("forge/azure: %s reported a remote URL for %s that this layer cannot derive a key from", c.d.host, asked)
	}
	if key.Host != c.d.host {
		return forge.Repository{}, fmt.Errorf("forge/azure: %s reported %s on host %q; a key derived from it would name a repository on a host this deployment has no driver for", c.d.host, asked, key.Host)
	}
	if err := c.d.Addressable(key); err != nil {
		return forge.Repository{}, fmt.Errorf("forge/azure: %s reported %s at an address this driver cannot act on: %w", c.d.host, asked, err)
	}
	return forge.Repository{
		Key:           key,
		HTTPS:         u.String(),
		SSH:           r.SSHURL,
		DefaultBranch: strings.TrimPrefix(r.DefaultBranch, "refs/heads/"),
		Private:       !strings.EqualFold(r.Project.Visibility, "public"),
	}, nil
}

// get fetches one repository and derives its record.
func (c *client) get(ctx context.Context, key repokey.Key) (repoJSON, forge.Repository, error) {
	at, err := c.d.coords(key)
	if err != nil {
		return repoJSON{}, forge.Repository{}, err
	}
	var r repoJSON
	if err := c.send(ctx, routeRepository.at(at.org, at.project, at.repo), nil, &r); err != nil {
		return repoJSON{}, forge.Repository{}, c.classify(err, key)
	}
	repo, err := c.repository(r, fmt.Sprintf("repository %q", key))
	if err != nil {
		return repoJSON{}, forge.Repository{}, err
	}
	return r, repo, nil
}

// bound is get for a VERDICT or a WRITE: the repository the forge answered
// about must be the one asked about. A record may name itself differently from
// the question; a permission, a hook or a deletion may not.
func (c *client) bound(ctx context.Context, key repokey.Key) (coords, repoJSON, error) {
	r, repo, err := c.get(ctx, key)
	if err != nil {
		return coords{}, repoJSON{}, err
	}
	if repo.Key != key {
		return coords{}, repoJSON{}, fmt.Errorf("forge/azure: %s was asked about %q and answered about %q", c.d.host, key, repo.Key)
	}
	at, err := c.d.coords(key)
	return at, r, err
}

func (c *client) Repository(ctx context.Context, key repokey.Key) (forge.Repository, error) {
	_, repo, err := c.get(ctx, key)
	return repo, err
}

type evaluation struct {
	SecurityNamespaceID string `json:"securityNamespaceId"`
	Token               string `json:"token"`
	Permissions         int    `json:"permissions"`
	Value               bool   `json:"value"`
}

type evaluationBatch struct {
	AlwaysAllowAdministrators bool         `json:"alwaysAllowAdministrators"`
	Evaluations               []evaluation `json:"evaluations"`
}

// Access asks the security service what THIS credential holds on the
// repository's ACL token, repoV2/{project id}/{repository id}. Azure DevOps
// returns no permissions with a repository, so this is two requests.
//
// Every answer is matched back to the question by token and bit rather than
// by position, and a bit the forge did not answer is an error — never a
// false — so a short answer cannot read as a denial.
func (c *client) Access(ctx context.Context, key repokey.Key) (forge.Access, error) {
	at, r, err := c.bound(ctx, key)
	if err != nil {
		return forge.AccessNone, err
	}
	token := "repoV2/" + r.Project.ID + "/" + r.ID
	bits := []int{permManagePermissions, permContribute, permRead}
	ask := evaluationBatch{}
	for _, b := range bits {
		ask.Evaluations = append(ask.Evaluations, evaluation{SecurityNamespaceID: gitNamespace, Token: token, Permissions: b})
	}
	var got evaluationBatch
	if err := c.send(ctx, routeEvaluate.at(at.org), ask, &got); err != nil {
		return forge.AccessNone, c.classify(err, key)
	}
	held := map[int]bool{}
	for _, e := range got.Evaluations {
		if strings.EqualFold(e.SecurityNamespaceID, gitNamespace) && strings.EqualFold(e.Token, token) {
			held[e.Permissions] = e.Value
		}
	}
	for _, b := range bits {
		if _, ok := held[b]; !ok {
			return forge.AccessNone, fmt.Errorf("forge/azure: %s did not answer permission %d on %q", c.d.host, b, key)
		}
	}
	switch {
	case held[permManagePermissions]:
		return forge.AccessAdmin, nil
	case held[permContribute]:
		return forge.AccessWrite, nil
	case held[permRead]:
		return forge.AccessRead, nil
	}
	return forge.AccessNone, nil
}

type repoList struct {
	Value []repoJSON `json:"value"`
}

// ListRepositories lists every repository readable in each organization this
// deployment answers in. Azure DevOps returns an organization's repositories
// in one answer, unpaged. A disabled repository, or one this layer cannot
// name, is skipped rather than yielded with a key nothing could act on.
func (c *client) ListRepositories(ctx context.Context, yield func(forge.Repository) bool) error {
	for _, org := range c.d.orgs {
		var out repoList
		if err := c.send(ctx, routeListRepositories.at(org), nil, &out); err != nil {
			return c.classify(err, repokey.Key{})
		}
		for i, r := range out.Value {
			if r.IsDisabled {
				continue
			}
			repo, err := c.repository(r, fmt.Sprintf("entry %d of the %q listing", i+1, org))
			if err != nil {
				continue
			}
			if !yield(repo) {
				return nil
			}
		}
	}
	return nil
}

type projectJSON struct {
	ID         string `json:"id"`
	Visibility string `json:"visibility"`
}

type createRequest struct {
	Name    string `json:"name"`
	Project struct {
		ID string `json:"id"`
	} `json:"project"`
}

// Create makes the repository at its key, inside the project the key names.
//
// VISIBILITY IS THE PROJECT'S on Azure DevOps, not the repository's, so a
// request for a private repository in a public project — or the reverse — is
// refused rather than created with the visibility nobody asked for. A
// repository has no description field, so CreateOptions.Description has
// nowhere to go and is not sent.
func (c *client) Create(ctx context.Context, key repokey.Key, opts forge.CreateOptions) (forge.Repository, error) {
	at, err := c.d.coords(key)
	if err != nil {
		return forge.Repository{}, err
	}
	var p projectJSON
	if err := c.send(ctx, routeProject.at(at.org, at.project), nil, &p); err != nil {
		return forge.Repository{}, c.classify(err, repokey.Key{})
	}
	if p.ID == "" {
		return forge.Repository{}, fmt.Errorf("forge/azure: %s described project %q with no id", c.d.host, at.project)
	}
	if public := strings.EqualFold(p.Visibility, "public"); public == opts.Private {
		return forge.Repository{}, fmt.Errorf("forge/azure: %q would be created in a %s project; on Azure DevOps visibility is the project's, so a repository asked for as private=%t cannot be made there", key, p.Visibility, opts.Private)
	}
	body := createRequest{Name: at.repo}
	body.Project.ID = p.ID
	var r repoJSON
	if err := c.send(ctx, routeCreate.at(at.org, at.project), body, &r); err != nil {
		return forge.Repository{}, c.classify(err, repokey.Key{})
	}
	repo, err := c.repository(r, fmt.Sprintf("repository %q", key))
	if err != nil {
		return forge.Repository{}, err
	}
	if repo.Key != key {
		return forge.Repository{}, fmt.Errorf("forge/azure: asked for %q and the forge created %q", key, repo.Key)
	}
	return repo, nil
}

// Delete removes the repository by the id of the one the key names — after
// checking the forge's answer names that key, because a delete through a
// rename is a delete of somebody else's repository.
func (c *client) Delete(ctx context.Context, key repokey.Key) error {
	at, r, err := c.bound(ctx, key)
	if err != nil {
		return err
	}
	if err := c.send(ctx, routeDelete.at(at.org, at.project, r.ID), nil, nil); err != nil {
		return c.classify(err, key)
	}
	return nil
}

// ─────────────────────────────── clone addresses ───────────────────────────────

// CloneAddress derives the address from the key with no request.
//
// HTTPS authenticates a PAT as the password and ignores the user name, so the
// one below is a label. SSH on a Server is ssh://{host}/{collection}/{project}/_git/{repo};
// on Services it is a DIFFERENT HOST, ssh.dev.azure.com, under a v3/ path —
// a good address, and a different repokey key, so it is refused here rather
// than handed out as an address for this key. Repository().SSH carries it.
func (c *client) CloneAddress(key repokey.Key, over forge.Protocol) (forge.Address, error) {
	at, err := c.d.coords(key)
	if err != nil {
		return forge.Address{}, err
	}
	escaped, err := escapedSegments([]string{at.org, at.project, "_git", at.repo})
	if err != nil {
		return forge.Address{}, err
	}
	if over == forge.SSH {
		if c.d.host == servicesHost {
			return forge.Address{}, fmt.Errorf("forge/azure: Azure DevOps Services serves SSH from ssh.dev.azure.com, another host, so no SSH address names %q; use the address Repository reports", key)
		}
		return forge.Address{URL: "ssh://git@" + c.d.host + "/" + escaped, SSHAccount: "git"}, nil
	}
	return forge.Address{URL: "https://" + c.d.host + "/" + escaped, HTTPUsername: "pat"}, nil
}
