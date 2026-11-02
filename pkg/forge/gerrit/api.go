package gerrit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
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
	// maxResponseBody bounds what this driver will decode: a response is a
	// stranger's bytes even when the stranger is a forge.
	maxResponseBody = 8 << 20

	// maxHostString bounds a string a HOST chose before it reaches an error.
	maxHostString = 256

	// maxDrainOnError is how much of a non-2xx body is read so the connection can
	// be reused. Gerrit's plain-text error bodies are never used.
	maxDrainOnError = 4 << 10

	// xssiGuard is the line Gerrit prefixes every JSON body with.
	xssiGuard = ")]}'"
)

// client holds the username, which is not a secret, and no copy of the
// password: only the closure writing the header does.
type client struct {
	d      *driver
	http   *http.Client
	user   string // "" for an anonymous client
	prefix string // "/a/" when authenticated, "/" when anonymous
}

var (
	_ forge.Client         = (*client)(nil)
	_ forge.Lister         = (*client)(nil)
	_ forge.Creator        = (*client)(nil)
	_ forge.Deleter        = (*client)(nil)
	_ forge.CloneAddresses = (*client)(nil)
)

// bind builds the client for one credential; nil is anonymous. The credential
// is attached by PinnedTransport and by nothing else.
func (d *driver) bind(b *basic) *client {
	c := &client{d: d, prefix: "/"}
	var attach func(http.Header)
	if b != nil {
		c.user, c.prefix = b.user, "/a/"
		user, password := b.user, b.password
		attach = func(h http.Header) { h.Set("Authorization", "Basic "+basicAuth(user, password)) }
	}
	c.http = &http.Client{
		CheckRedirect: noRedirect,
		Transport:     forge.PinnedTransport(d.host, d.rt, attach),
	}
	return c
}

func basicAuth(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

// No redirect is followed, for the gitea driver's reason; Gerrit's REST API
// does not redirect.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (c *client) Host() string { return c.d.host }

// statusError is a non-2xx answer, carrying the request and the code and
// nothing the host chose.
type statusError struct {
	Method string
	Path   string
	Status int
}

func (e *statusError) Refused() bool { return e.Status >= 400 && e.Status < 500 }

func (e *statusError) Error() string {
	return fmt.Sprintf("forge/gerrit: %s %s answered %d %s", e.Method, e.Path, e.Status, http.StatusText(e.Status))
}

func statusOf(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

// send issues one request and decodes a 2xx body into out. It is the only
// place in the package that builds an *http.Request.
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
	u, err := url.Parse(c.d.scheme + "://" + c.d.host + c.prefix + p)
	if err != nil {
		return fmt.Errorf("forge/gerrit: %s %s is not a URL: %w", method, p, err)
	}
	u.RawQuery = query

	var body io.Reader
	if in != nil {
		enc, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("forge/gerrit: encoding the %s %s request: %w", method, p, err)
		}
		body = bytes.NewReader(enc)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return fmt.Errorf("forge/gerrit: building %s %s: %w", method, p, err)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("forge/gerrit: %s %s: %w", method, p, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainOnError))
		return &statusError{Method: method, Path: p, Status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := decode(io.LimitReader(resp.Body, maxResponseBody), out); err != nil {
		return fmt.Errorf("forge/gerrit: %s %s answered a body this driver cannot read: %w", method, p, err)
	}
	return nil
}

// decode removes the XSSI guard line and decodes what follows. A body without
// the guard is decoded as it is: the guard protects browsers, and its absence
// says nothing about the JSON.
func decode(r io.Reader, out any) error {
	br := bufio.NewReader(r)
	head, err := br.Peek(len(xssiGuard))
	if err == nil && string(head) == xssiGuard {
		if _, err := br.ReadString('\n'); err != nil {
			return errors.New("the XSSI guard line has no end")
		}
	}
	return json.NewDecoder(br).Decode(out)
}

// 401 is the credential refused; 404 is no such project, or one this credential
// may not see. Anything else, a 403 included, stays an error.
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

// projectJSON is what this driver reads out of a ProjectInfo. The id, the
// parent, the labels and the web links are deliberately absent.
type projectJSON struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// More is set on the LAST entry of a listing page when there are more.
	More bool `json:"_more_projects"`
}

// accessJSON is the caller's own ProjectAccessInfo. is_owner is absent rather
// than false when the caller does not own the project, and absent decodes to
// false, which is AccessRead for a project the caller can see.
type accessJSON struct {
	IsOwner bool                     `json:"is_owner"`
	Local   map[string]accessSection `json:"local"`
}

type accessSection struct {
	Permissions map[string]permissionJSON `json:"permissions"`
}

type permissionJSON struct {
	Exclusive bool `json:"exclusive"`
}

// restricted is whether the project's own config makes reads exclusive on the
// branches, which is what Create writes for a private project. It is
// METADATA, read only where the caller may see the config, and never
// authorization: it says what the project declares, not who can read it.
func (a accessJSON) restricted() bool {
	s, ok := a.Local["refs/heads/*"]
	if !ok {
		return false
	}
	return s.Permissions["read"].Exclusive
}

// access maps Gerrit's answer onto tau's rungs. See the package comment for
// why AccessWrite is never returned.
func (a accessJSON) access() forge.Access {
	if a.IsOwner {
		return forge.AccessAdmin
	}
	return forge.AccessRead
}

// repository derives the key from the project name the forge returned, never
// from the argument. Errors name asked, not the forge's chosen name.
func (c *client) repository(name, head string, private bool, asked string) (forge.Repository, error) {
	key, escaped, err := c.keyOf(name, asked)
	if err != nil {
		return forge.Repository{}, err
	}
	branch, ok := strings.CutPrefix(head, "refs/heads/")
	if !ok || branch == "" {
		return forge.Repository{}, fmt.Errorf("forge/gerrit: %s reported a HEAD for %s that is not a branch", c.d.host, asked)
	}
	repo := forge.Repository{
		Key:           key,
		HTTPS:         c.d.scheme + "://" + c.d.host + "/" + escaped,
		DefaultBranch: branch,
		Private:       private,
	}
	if c.user != "" {
		repo.SSH = c.sshURL(escaped)
	}
	return repo, nil
}

// keyOf is the key a project name the forge returned derives, and the escaped
// path a clone of it uses.
func (c *client) keyOf(name, asked string) (repokey.Key, string, error) {
	switch {
	case name == "":
		return repokey.Key{}, "", fmt.Errorf("forge/gerrit: %s described %s with no name, so there is nothing to derive a key from", c.d.host, asked)
	case len(name) > maxHostString:
		return repokey.Key{}, "", fmt.Errorf("forge/gerrit: %s described %s with a %d-byte name, which is not one", c.d.host, asked, len(name))
	case name != strings.ToLower(name):
		return repokey.Key{}, "", fmt.Errorf("forge/gerrit: %s described %s by a name that is not lowercase; Gerrit's names are case-sensitive and this layer's keys are not, so it has no key that addresses only it", c.d.host, asked)
	}
	escaped, err := projectPath(name)
	if err != nil {
		return repokey.Key{}, "", fmt.Errorf("forge/gerrit: %s described %s by a name that is not a path", c.d.host, asked)
	}
	key, err := repokey.Parse(c.d.scheme + "://" + c.d.host + "/" + escaped)
	if err != nil || key.FullName() != name {
		return repokey.Key{}, "", fmt.Errorf("forge/gerrit: %s described %s by a name this layer cannot derive a key from", c.d.host, asked)
	}
	return key, escaped, nil
}

// projectPath is a project name as a URL path, each "/"-separated part
// escaped by segment(): the path a clone uses, as opposed to the one segment
// the REST API takes.
func projectPath(name string) (string, error) {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		seg, err := segment(p)
		if err != nil {
			return "", err
		}
		parts[i] = seg
	}
	return strings.Join(parts, "/"), nil
}

// The ssh account is the caller's username: Gerrit's sshd authenticates
// accounts, not a shared "git" user. The port is the key's, though Gerrit's sshd
// listens on 29418; a spelling with 29418 would derive another key, so a
// deployment cloning over ssh supplies its address.
func (c *client) sshURL(escaped string) string {
	return "ssh://" + url.User(c.user).String() + "@" + c.d.host + "/" + escaped
}
