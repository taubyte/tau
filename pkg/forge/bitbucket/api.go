package bitbucket

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
	// stranger's bytes even when the stranger is a forge.
	maxResponseBody = 8 << 20

	// maxHostString bounds a string a HOST chose before it reaches an error
	// message: a forge that can pick how long a log line is can pick how much of
	// a disk it costs.
	maxHostString = 256

	// maxDrainOnError is how much of a non-2xx body is read so the connection
	// can be reused. The body itself is never read for meaning.
	maxDrainOnError = 4 << 10
)

// client holds no copy of the credential: only the closure in bind that writes
// the header does.
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

// The credential is pinned to the API host and no redirect is followed: a
// followed one lands a write wherever the forge named.
func (d *driver) bind(token string) *client {
	return &client{
		d: d,
		http: &http.Client{
			CheckRedirect: noRedirect,
			Transport: forge.PinnedTransport(d.apiHost, d.rt, func(h http.Header) {
				if token != "" {
					// Repository, project and workspace access tokens and OAuth
					// access tokens are all bearer tokens on Bitbucket Cloud.
					h.Set("Authorization", "Bearer "+token)
				}
			}),
		},
	}
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (c *client) Host() string { return c.d.host }

// statusError is a non-2xx answer. It names the method, the path this driver
// built and the status, and NEVER the body: a host that can choose what lands
// in a log can choose to put the caller's own credential there.
type statusError struct {
	Method string
	Path   string
	Status int
}

func (e *statusError) Refused() bool { return e.Status >= 400 && e.Status < 500 }

func (e *statusError) Error() string {
	return fmt.Sprintf("forge/bitbucket: %s %s answered %d %s", e.Method, e.Path, e.Status, http.StatusText(e.Status))
}

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
		return fmt.Errorf("forge/bitbucket: %s %s is not a URL: %w", method, path, err)
	}
	u.RawQuery = query

	var body io.Reader
	if in != nil {
		enc, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("forge/bitbucket: encoding the %s %s request: %w", method, path, err)
		}
		body = bytes.NewReader(enc)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return fmt.Errorf("forge/bitbucket: building %s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", acceptJSON)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("forge/bitbucket: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The drain is best-effort connection reuse; its error changes nothing.
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainOnError))
		return &statusError{Method: method, Path: path, Status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		return fmt.Errorf("forge/bitbucket: %s %s answered a body this driver cannot read: %w", method, path, err)
	}
	return nil
}

// 401 is the credential refused, 404 no such repository (or a private one this
// credential may not see). A 403 is a missing scope or a refusing workspace,
// and stays what it is.
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

func (c *client) classifyResource(err error) error {
	return c.classify(err, repokey.Key{})
}

type repoJSON struct {
	FullName   string        `json:"full_name"`
	IsPrivate  bool          `json:"is_private"`
	MainBranch *branchJSON   `json:"mainbranch"`
	Links      repoLinksJSON `json:"links"`
}

type repoLinksJSON struct {
	Clone []cloneLinkJSON `json:"clone"`
}

type cloneLinkJSON struct {
	Name string `json:"name"`
	Href string `json:"href"`
}

type branchJSON struct {
	Name string `json:"name"`
}

func (r repoJSON) clone(name string) string {
	for _, l := range r.Links.Clone {
		if l.Name == name {
			return l.Href
		}
	}
	return ""
}

// repository derives the key from the https clone link the forge returned.
// Bitbucket writes the caller's username into that link; it is removed from the
// address handed on.
func (c *client) repository(r repoJSON, asked string) (forge.Repository, error) {
	href := r.clone("https")
	switch {
	case href == "":
		return forge.Repository{}, fmt.Errorf("forge/bitbucket: %s described %s with no https clone link, so there is nothing to derive a key from", c.d.host, asked)
	case len(href) > maxHostString:
		return forge.Repository{}, fmt.Errorf("forge/bitbucket: %s described %s with a %d-byte clone link, which is not one", c.d.host, asked, len(href))
	}
	// THE REASON IS NOT CARRIED: repokey quotes what it was given, and a clone
	// link with a password in it would put that password in a tau log.
	key, err := repokey.Parse(href)
	if err != nil {
		return forge.Repository{}, fmt.Errorf("forge/bitbucket: %s reported a clone link for %s that this layer cannot derive a key from", c.d.host, asked)
	}
	if key.Host != c.d.host {
		return forge.Repository{}, fmt.Errorf("forge/bitbucket: %s reported %s on host %q; a key derived from it would name a repository on a host this deployment has no driver for", c.d.host, asked, key.Host)
	}
	u, err := url.Parse(href)
	if err != nil {
		return forge.Repository{}, fmt.Errorf("forge/bitbucket: %s reported a clone link for %s that is not a URL", c.d.host, asked)
	}
	u.User = nil
	repo := forge.Repository{
		Key:     key,
		HTTPS:   u.String(),
		SSH:     r.clone("ssh"),
		Private: r.IsPrivate,
	}
	if r.MainBranch != nil {
		repo.DefaultBranch = r.MainBranch.Name
	}
	return repo, nil
}
