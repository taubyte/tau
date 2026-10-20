package bitbucketserver

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
	// maxResponseBody bounds what this driver will decode.
	maxResponseBody = 8 << 20

	// maxHostString bounds a string a HOST chose before it reaches an error.
	maxHostString = 256

	// maxErrorBody is how much of a non-2xx body is read: enough for the
	// exception a 401 names, and so the connection can be reused.
	maxErrorBody = 4 << 10

	// authorisationException is what Bitbucket names on a 401 that refuses an
	// authenticated caller a permission, rather than refusing the credential.
	authorisationException = "AuthorisationException"
)

// client is a driver acting as one credential. It holds no copy of the
// credential: the token reaches exactly one place, the closure that writes the
// header.
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

// bind pins the credential to the host and follows no redirect: nothing tau
// asks is answered with one it needs, and a followed one is a write landing
// wherever the forge named.
func (d *driver) bind(token string) *client {
	return &client{
		d: d,
		http: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: forge.PinnedTransport(d.host, d.rt, func(h http.Header) {
				if token != "" {
					// An HTTP access token, personal, project or repository, is a
					// bearer token.
					h.Set("Authorization", "Bearer "+token)
				}
			}),
		},
	}
}

func (c *client) Host() string { return c.d.host }

// statusError is a non-2xx answer. It names the method, the path and the
// status, never the body.
type statusError struct {
	Method string
	Path   string
	Status int
	// Authz says a 401 named an AuthorisationException: the caller is who
	// they say and may not do this.
	Authz bool
}

func (e *statusError) Error() string {
	reason := http.StatusText(e.Status)
	if e.Authz {
		reason = "the credential holds no permission for this"
	}
	return fmt.Sprintf("forge/bitbucketserver: %s %s answered %d %s", e.Method, e.Path, e.Status, reason)
}

// errorBody is the one thing read out of a refusal: which exceptions it names.
type errorBody struct {
	Errors []struct {
		ExceptionName string `json:"exceptionName"`
	} `json:"errors"`
}

func (b errorBody) authz() bool {
	for _, e := range b.Errors {
		if strings.HasSuffix(e.ExceptionName, "."+authorisationException) || e.ExceptionName == authorisationException {
			return true
		}
	}
	return false
}

// send is the ONE place this driver builds an HTTP request. out may be a
// *string, for the one route that answers plain text.
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
		return fmt.Errorf("forge/bitbucketserver: %s %s is not a URL: %w", method, path, err)
	}
	u.RawQuery = query

	var body io.Reader
	if in != nil {
		enc, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("forge/bitbucketserver: encoding the %s %s request: %w", method, path, err)
		}
		body = bytes.NewReader(enc)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return fmt.Errorf("forge/bitbucketserver: building %s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("forge/bitbucketserver: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		se := &statusError{Method: method, Path: path, Status: resp.StatusCode}
		var eb errorBody
		// Best effort: a refusal whose body is unreadable is still a refusal.
		if json.NewDecoder(io.LimitReader(resp.Body, maxErrorBody)).Decode(&eb) == nil {
			se.Authz = resp.StatusCode == http.StatusUnauthorized && eb.authz()
		}
		return se
	}
	if out == nil {
		return nil
	}
	if s, ok := out.(*string); ok {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHostString+1))
		if err != nil {
			return fmt.Errorf("forge/bitbucketserver: %s %s: %w", method, path, err)
		}
		*s = string(raw)
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		return fmt.Errorf("forge/bitbucketserver: %s %s answered a body this driver cannot read: %w", method, path, err)
	}
	return nil
}

// classify maps a status onto forge's taxonomy:
//
//	401  the forge refused this credential — unless it named an
//	     AuthorisationException, which is a permission refused to a known
//	     caller and is returned as what it is
//	404  the host serves no such repository, or none this credential may see
func (c *client) classify(err error, key repokey.Key) error {
	var se *statusError
	if !errors.As(err, &se) {
		return err
	}
	switch {
	case se.Status == http.StatusUnauthorized && !se.Authz:
		return &forge.CredentialError{Host: c.d.host, Reason: "the forge refused this credential"}
	case se.Status == http.StatusNotFound && key.Name != "":
		return &forge.NotFoundError{Host: c.d.host, Key: key}
	}
	return err
}

// link is one of Bitbucket's named links.
type link struct {
	Href string `json:"href"`
	Name string `json:"name"`
}

// repoJSON is the whole of what this driver reads out of a repository.
type repoJSON struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Public  bool   `json:"public"`
	Project struct {
		Key string `json:"key"`
	} `json:"project"`
	Links struct {
		Clone []link `json:"clone"`
	} `json:"links"`
}

func (r repoJSON) clone(name string) string {
	for _, l := range r.Links.Clone {
		if l.Name == name {
			return l.Href
		}
	}
	return ""
}

// repository turns Bitbucket's answer into forge's, deriving the key from the
// https clone link the forge returned — through this driver's own spelling —
// and checking that the project and slug the answer names are the repository
// that link is.
//
// Bitbucket writes the caller's username into that link ("https://alice@…").
// It is an identity and not a secret, and it is removed from the address
// handed on: the identity a clone spends is the platform's to choose.
func (c *client) repository(r repoJSON, asked string) (forge.Repository, error) {
	href := r.clone("http")
	switch {
	case href == "":
		return forge.Repository{}, fmt.Errorf("forge/bitbucketserver: %s described %s with no http clone link, so there is nothing to derive a key from", c.d.host, asked)
	case len(href) > maxHostString:
		return forge.Repository{}, fmt.Errorf("forge/bitbucketserver: %s described %s with a %d-byte clone link, which is not one", c.d.host, asked, len(href))
	}
	// The reason is not carried: repokey quotes what it was given, and a link
	// with a password in it would put that password in a log.
	parsed, err := repokey.Parse(href)
	if err != nil {
		return forge.Repository{}, fmt.Errorf("forge/bitbucketserver: %s reported a clone link for %s that this layer cannot derive a key from", c.d.host, asked)
	}
	key, ok := c.d.Canonical(parsed)
	if !ok || key.Host != c.d.host {
		return forge.Repository{}, fmt.Errorf("forge/bitbucketserver: %s reported a clone link for %s that is not one of its own addresses", c.d.host, asked)
	}
	named, err := c.d.keyOf(r.Project.Key, r.Slug)
	if err != nil || named != key {
		return forge.Repository{}, fmt.Errorf("forge/bitbucketserver: %s described %s as one repository and linked its clone to another", c.d.host, asked)
	}
	u, err := url.Parse(href)
	if err != nil {
		return forge.Repository{}, fmt.Errorf("forge/bitbucketserver: %s reported a clone link for %s that is not a URL", c.d.host, asked)
	}
	u.User = nil
	return forge.Repository{
		Key:     key,
		HTTPS:   u.String(),
		SSH:     r.clone("ssh"),
		Private: !r.Public,
	}, nil
}
