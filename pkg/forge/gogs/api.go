package gogs

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
	// maxResponseBody bounds what this driver will decode. A response is a
	// stranger's bytes even when the stranger is a forge: an unbounded decode is
	// an unbounded allocation decided by whoever is answering.
	maxResponseBody = 8 << 20

	// maxHostString bounds a string a HOST chose before it reaches an error
	// message. A forge that can pick how long a log line is can pick how much of
	// a disk it costs, and a clone URL and a full name are both its to write.
	maxHostString = 256

	// maxDrainOnError is how much of a non-2xx body is read before the
	// connection is returned to the pool. The body itself is never used — see
	// the package comment — but reading a little of it is what makes the
	// connection reusable instead of dropped.
	maxDrainOnError = 4 << 10
)

// client is stateless and holds no copy of the credential: only the closure
// writing the header does, so nothing can render it.
type client struct {
	d    *driver
	http *http.Client
}

var (
	_ forge.Client         = (*client)(nil)
	_ forge.Lister         = (*client)(nil)
	_ forge.Creator        = (*client)(nil)
	_ forge.Deleter        = (*client)(nil)
	_ forge.DeployKeys     = (*client)(nil)
	_ forge.PushHooks      = (*client)(nil)
	_ forge.CloneAddresses = (*client)(nil)
)

// The credential is attached by PinnedTransport and nothing else, so no request
// carries it off the API host.
func (d *driver) bind(token string) *client {
	return &client{
		d: d,
		http: &http.Client{
			CheckRedirect: noRedirect,
			Transport: forge.PinnedTransport(d.host, d.rt, func(h http.Header) {
				// An anonymous session sends nothing, since "token " turns a public read into a
				// 401. "token" is the spelling Gogs documents.
				if token != "" {
					h.Set("Authorization", "token "+token)
				}
			}),
		},
	}
}

// This driver follows no redirect. Gogs keeps redirects for renamed and
// transferred repositories, which Go replays; the pin permits a same-host hop,
// so a request for one repository would be answered by another while returning
// the caller's key, and a transferred name is free to re-register. A write
// cannot be undone by checking afterwards.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (c *client) Host() string { return c.d.host }

// statusError carries the request and its code, nothing the host chose.
type statusError struct {
	Method string
	Path   string
	Status int
}

func (e *statusError) Refused() bool { return e.Status >= 400 && e.Status < 500 }

func (e *statusError) Error() string {
	return fmt.Sprintf("forge/gogs: %s %s answered %d %s", e.Method, e.Path, e.Status, http.StatusText(e.Status))
}

// send takes a route, not a path: it is the only place an *http.Request is
// built. A non-2xx is a *statusError; a transport failure stays what it was.
func (c *client) send(ctx context.Context, rq request, in, out any) error {
	path, err := rq.path()
	if err != nil {
		return err
	}
	method := rq.route.method
	query, err := rq.rawQuery()
	if err != nil {
		return err
	}

	u, err := url.Parse(c.d.base.String() + path)
	if err != nil {
		return fmt.Errorf("forge/gogs: %s %s is not a URL: %w", method, path, err)
	}
	u.RawQuery = query

	var body io.Reader
	if in != nil {
		enc, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("forge/gogs: encoding the %s %s request: %w", method, path, err)
		}
		body = bytes.NewReader(enc)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return fmt.Errorf("forge/gogs: building %s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", acceptJSON)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("forge/gogs: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainOnError))
		return &statusError{Method: method, Path: path, Status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		return fmt.Errorf("forge/gogs: %s %s answered a body this driver cannot read: %w", method, path, err)
	}
	return nil
}

// 401 is the credential refused, never a verdict on the repository; 404 is no
// such repository, or a private one this credential may not see. Anything else,
// a 403 included, stays an error.
func (c *client) classify(err error, key repokey.Key) error {
	var se *statusError
	if !errors.As(err, &se) {
		return err
	}
	switch {
	case se.Status == http.StatusUnauthorized:
		return &forge.CredentialError{Host: c.d.host, Reason: "the forge refused this credential"}
	case se.Status == http.StatusNotFound && key.Name != "":
		return &forge.NotFoundError{Host: c.d.host, Key: key}
	}
	return err
}

// classifyResource is classify for something UNDER a repository — a hook, a
// deploy key. A 404 there is the resource and not the repository, and calling
// it *NotFoundError would say the host serves no repository that is plainly
// there.
func (c *client) classifyResource(err error) error {
	return c.classify(err, repokey.Key{})
}

// repoJSON is what this driver reads out of a repository. Everything else
// Gogs sends — the numeric id, the owner object, the counts, the URLs of its
// features — is deliberately absent: a field that is not parsed is a field
// nothing later can come to depend on.
type repoJSON struct {
	FullName      string      `json:"full_name"`
	CloneURL      string      `json:"clone_url"`
	SSHURL        string      `json:"ssh_url"`
	DefaultBranch string      `json:"default_branch"`
	Private       bool        `json:"private"`
	Permissions   permissions `json:"permissions"`
}

// permissions is the caller's own grant, which Gogs returns WITH the
// repository — so the authorization question costs no second request. It is
// absent for an unauthenticated read, and absent decodes to all-false, which is
// AccessNone.
type permissions struct {
	Admin bool `json:"admin"`
	Push  bool `json:"push"`
	Pull  bool `json:"pull"`
}

// push or admin is write. pull is AccessRead, not AccessWrite: a public
// repository returns pull:true to every token.
func (p permissions) access() forge.Access {
	switch {
	case p.Admin:
		return forge.AccessAdmin
	case p.Push:
		return forge.AccessWrite
	case p.Pull:
		return forge.AccessRead
	}
	return forge.AccessNone
}

// repository derives the key from the clone URL the forge returned. Errors name
// asked, never the forge's full_name or repokey's reason: repokey quotes what
// it was given, and a clone URL with an identity half would put a credential
// the forge chose into tau logs. A clone URL on another host is refused: it
// would file the repository where nothing can act on it, and HTTPS is where a
// builder spends a credential. The SSH URL passes unchecked; nothing clones it
// with a bearer credential.
func (c *client) repository(r repoJSON, asked string) (forge.Repository, error) {
	switch {
	case r.CloneURL == "":
		return forge.Repository{}, fmt.Errorf("forge/gogs: %s described %s with no clone URL, so there is nothing to derive a key from", c.d.host, asked)
	case len(r.CloneURL) > maxHostString:
		return forge.Repository{}, fmt.Errorf("forge/gogs: %s described %s with a %d-byte clone URL, which is not one", c.d.host, asked, len(r.CloneURL))
	}
	key, err := repokey.Parse(r.CloneURL)
	if err != nil {
		return forge.Repository{}, fmt.Errorf("forge/gogs: %s reported a clone URL for %s that this layer cannot derive a key from", c.d.host, asked)
	}
	if key.Host != c.d.host {
		return forge.Repository{}, fmt.Errorf("forge/gogs: %s reported %s on host %q; a key derived from it would name a repository on a host this deployment has no driver for, and the address would be one a clone spends a credential at", c.d.host, asked, key.Host)
	}
	return forge.Repository{
		Key:           key,
		HTTPS:         r.CloneURL,
		SSH:           r.SSHURL,
		DefaultBranch: r.DefaultBranch,
		Private:       r.Private,
	}, nil
}
