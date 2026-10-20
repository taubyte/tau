package onedev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const (
	// maxResponseBody bounds what this driver will decode: a response is a
	// stranger's bytes even when the stranger is a forge.
	maxResponseBody = 8 << 20

	// maxHostString bounds a string a HOST chose before it reaches an error.
	maxHostString = 256

	// maxDrainOnError is how much of a non-2xx body is read so the connection
	// is reused. The body itself is never used.
	maxDrainOnError = 4 << 10
)

// client is a driver acting as one credential. It is stateless and holds no
// copy of the credential: the token reaches one closure, which writes the
// header, and nothing else.
type client struct {
	d    *driver
	http *http.Client
	// anonymous is whether this client sends no credential at all. OneDev
	// answers an anonymous caller 401 for everything it may not see, and for
	// THAT caller a 401 is "not visible", not "credential refused".
	anonymous bool
}

var (
	_ forge.Client         = (*client)(nil)
	_ forge.Lister         = (*client)(nil)
	_ forge.Creator        = (*client)(nil)
	_ forge.Deleter        = (*client)(nil)
	_ forge.PushHooks      = (*client)(nil)
	_ forge.CloneAddresses = (*client)(nil)
)

// bind builds the client for one token. Bearer is what OneDev's REST API and
// its git endpoints both accept for an access token.
func (d *driver) bind(token string) *client {
	return &client{
		d:         d,
		anonymous: token == "",
		http: &http.Client{
			CheckRedirect: noRedirect,
			Transport: forge.PinnedTransport(d.host, d.rt, func(h http.Header) {
				if token != "" {
					h.Set("Authorization", "Bearer "+token)
				}
			}),
		},
	}
}

// noRedirect: THIS DRIVER FOLLOWS NO REDIRECT AT ALL, the gitea driver's rule
// for its reasons. OneDev has no reason to send one to an API or git client,
// and a followed hop is a question answered about a repository nobody named.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (c *client) Host() string { return c.d.host }

// statusError is a non-2xx answer: the request this driver made and the code
// it got back, and nothing the host chose.
type statusError struct {
	Method string
	Path   string
	Status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("forge/onedev: %s %s answered %d %s", e.Method, e.Path, e.Status, http.StatusText(e.Status))
}

func statusOf(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

// send issues one request and decodes a 2xx JSON body into out. It is the only
// place in the package an *http.Request is built.
func (c *client) send(ctx context.Context, rq request, in, out any) error {
	body, err := c.do(ctx, rq, in)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("forge/onedev: %s %s answered a body this driver cannot read: %w", rq.route.method, rq.route.template, err)
	}
	return nil
}

// do is send without the decode: the bytes of a 2xx answer, bounded.
func (c *client) do(ctx context.Context, rq request, in any) ([]byte, error) {
	p, err := rq.path()
	if err != nil {
		return nil, err
	}
	method := rq.route.method
	query, err := rq.rawQuery()
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(c.d.base.String() + p)
	if err != nil {
		return nil, fmt.Errorf("forge/onedev: %s %s is not a URL: %w", method, p, err)
	}
	u.RawQuery = query

	var reader io.Reader
	if in != nil {
		enc, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("forge/onedev: encoding the %s %s request: %w", method, p, err)
		}
		reader = bytes.NewReader(enc)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("forge/onedev: building %s %s: %w", method, p, err)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("forge/onedev: %s %s: %w", method, p, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainOnError))
		return nil, &statusError{Method: method, Path: p, Status: resp.StatusCode}
	}
	// One byte past the cap is read so an oversized answer is refused rather
	// than silently truncated into something that parses.
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return nil, fmt.Errorf("forge/onedev: reading the answer to %s %s: %w", method, p, err)
	}
	if len(b) > maxResponseBody {
		return nil, fmt.Errorf("forge/onedev: %s %s answered more than %d bytes", method, p, maxResponseBody)
	}
	return b, nil
}

// classify maps a failure about ONE REPOSITORY into the taxonomy.
//
//	401  the credential was refused — unless there is none, where it is
//	     OneDev hiding what an anonymous caller may not see
//	404  the host serves no such project, which on OneDev also covers one this
//	     credential may not see
//
// Everything else stays an error and is not a verdict.
func (c *client) classify(err error, key repokey.Key) error {
	var se *statusError
	if !errors.As(err, &se) {
		return err
	}
	switch {
	case se.Status == http.StatusUnauthorized && c.anonymous && key.Name != "":
		return &forge.NotFoundError{Host: c.d.host, Key: key}
	case se.Status == http.StatusUnauthorized:
		return &forge.CredentialError{Host: c.d.host, Reason: "the forge refused this credential"}
	case se.Status == http.StatusNotFound && key.Name != "":
		return &forge.NotFoundError{Host: c.d.host, Key: key}
	}
	return err
}

// projectID resolves the path a key names to the number every other endpoint
// takes. It is the first request of every keyed operation, and it is the one
// OneDev answers 404 for a project that is absent or that this credential may
// not see.
func (c *client) projectID(ctx context.Context, key repokey.Key) (int64, error) {
	var id int64
	if err := c.send(ctx, routeProjectID.at(key.FullName()), nil, &id); err != nil {
		return 0, c.classify(err, key)
	}
	if id <= 0 {
		return 0, fmt.Errorf("forge/onedev: %s resolved %q to no project", c.d.host, key)
	}
	return id, nil
}

type cloneURLJSON struct {
	HTTP string `json:"http"`
	SSH  string `json:"ssh"`
}

// cloneKey fetches the clone addresses OneDev gives project id and derives the
// key from the HTTP one — the forge naming the repository, not the caller.
//
// asked is what tau called the project, and every error names it rather than
// the string the forge sent, and carries no reason repokey gave: repokey
// quotes what it was handed, and a clone URL with an identity half would be
// the forge writing a credential of its choosing into a tau log.
//
// A clone URL on another host is refused, for the gitea driver's reason: the
// key would name a host nothing here can act on, and the address is where a
// builder spends a credential. The SSH address is passed through unchecked:
// OneDev serves ssh on its own port, which is a different repokey host and a
// good clone address.
func (c *client) cloneKey(ctx context.Context, id int64, asked string) (repokey.Key, cloneURLJSON, error) {
	var cu cloneURLJSON
	if err := c.send(ctx, routeCloneURL.at(idSegment(id)), nil, &cu); err != nil {
		return repokey.Key{}, cloneURLJSON{}, c.classify(err, repokey.Key{})
	}
	switch {
	case cu.HTTP == "":
		return repokey.Key{}, cloneURLJSON{}, fmt.Errorf("forge/onedev: %s described %s with no clone URL, so there is nothing to derive a key from", c.d.host, asked)
	case len(cu.HTTP) > maxHostString:
		return repokey.Key{}, cloneURLJSON{}, fmt.Errorf("forge/onedev: %s described %s with a %d-byte clone URL, which is not one", c.d.host, asked, len(cu.HTTP))
	}
	key, err := repokey.Parse(cu.HTTP)
	if err != nil {
		return repokey.Key{}, cloneURLJSON{}, fmt.Errorf("forge/onedev: %s reported a clone URL for %s that this layer cannot derive a key from", c.d.host, asked)
	}
	if key.Host != c.d.host {
		return repokey.Key{}, cloneURLJSON{}, fmt.Errorf("forge/onedev: %s reported %s on host %q; a key derived from it would name a repository on a host this deployment has no driver for, and the address would be one a clone spends a credential at", c.d.host, asked, key.Host)
	}
	return key, cu, nil
}

// defaultBranch is the project's default branch, or ok=false when this
// credential may not read the project's code. OneDev answers an empty
// repository with 204 and no body, which is a readable repository with no
// branch yet.
func (c *client) defaultBranch(ctx context.Context, id int64) (string, bool, error) {
	b, err := c.do(ctx, routeDefaultBranch.at(idSegment(id)), nil)
	switch {
	case statusOf(err) == http.StatusForbidden:
		return "", false, nil
	case err != nil:
		return "", false, c.classify(err, repokey.Key{})
	}
	b = bytes.TrimSpace(b)
	// OneDev writes the name bare, as text; a JSON string is read too, so the
	// answer means one thing whichever way a release spells it.
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return "", false, fmt.Errorf("forge/onedev: the default branch is not a readable string: %w", err)
		}
		return s, true, nil
	}
	if len(b) > maxHostString {
		return "", false, fmt.Errorf("forge/onedev: a %d-byte default branch is not one", len(b))
	}
	return string(b), true, nil
}

// record is the forge.Repository for project id, readable by this credential.
// ok=false is a project this credential reaches without being able to read its
// code, which is not a repository it can do anything with.
func (c *client) record(ctx context.Context, id int64, asked string) (forge.Repository, bool, error) {
	key, cu, err := c.cloneKey(ctx, id, asked)
	if err != nil {
		return forge.Repository{}, false, err
	}
	branch, ok, err := c.defaultBranch(ctx, id)
	if err != nil || !ok {
		return forge.Repository{}, false, err
	}
	private, err := c.private(ctx, key)
	if err != nil {
		return forge.Repository{}, false, err
	}
	return forge.Repository{Key: key, HTTPS: cu.HTTP, SSH: cu.SSH, DefaultBranch: branch, Private: private}, true, nil
}

// private is MEASURED, because OneDev has no visibility field: who may read a
// project is a set of role grants, inherited down the tree, and the instance
// decides whether anonymous access exists at all. A project is reported
// private unless a caller holding NO credential can resolve it — the one
// definition that means the same on every instance. Metadata only; Access is
// the authorization answer.
func (c *client) private(ctx context.Context, key repokey.Key) (bool, error) {
	if c.anonymous {
		return false, nil
	}
	_, err := c.d.bind("").projectID(ctx, key)
	var nf *forge.NotFoundError
	switch {
	case err == nil:
		return false, nil
	case errors.As(err, &nf):
		return true, nil
	}
	return false, fmt.Errorf("forge/onedev: whether %q is visible without a credential could not be established: %w", key, err)
}
