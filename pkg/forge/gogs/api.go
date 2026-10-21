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

// client is a driver acting as one credential. It is stateless: there is no
// "current repository", so no method can silently depend on a fetch having
// happened first, which is the shape v1's client has and the reason its
// CreateDeployKey has a "no repository selected" error at all.
//
// AND IT HOLDS NO COPY OF THE CREDENTIAL. The credential reaches exactly one
// place, the closure below that writes the header, which captures the parameter
// — so there is no field for %v to render, nothing for a struct dump to carry
// into a log, and nothing to forget to redact. forge.Credential goes to the
// trouble of closing every verb through enclosing structs, slices, maps and
// pointers; a driver that unwraps one at the door and keeps the string beside
// it undoes that in one line, and the line bought nothing.
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

// bind builds the client for one token. The credential is attached by
// PinnedTransport and by nothing else, so there is no path by which a request
// this driver builds carries it to a host other than the API host, and no path
// by which a redirect carries it off one.
func (d *driver) bind(token string) *client {
	return &client{
		d: d,
		http: &http.Client{
			CheckRedirect: noRedirect,
			Transport: forge.PinnedTransport(d.host, d.rt, func(h http.Header) {
				// An anonymous session binds an empty credential; sending
				// "token " would turn a public read into a 401. "token" and not
				// "Bearer" because it is the spelling Gogs documents.
				if token != "" {
					h.Set("Authorization", "token "+token)
				}
			}),
		},
	}
}

// noRedirect stops at the 3xx instead of replaying the request wherever the
// answer points. THIS DRIVER FOLLOWS NO REDIRECT AT ALL.
//
// A forge that answers a request for a renamed or TRANSFERRED repository with a
// redirect to its current path — Gogs keeps redirects for both — invites Go to
// replay method and body there. PinnedTransport permits that hop and must: it is the
// same host, and a per-hop host check is the whole of what a pin can decide.
// So without this, a request built for one repository is answered by another —
// the permissions read are that other repository's, the hook is installed on
// it, the deletion lands on it — while every value this driver returns still
// carries the key the CALLER named. A transferred name is free to re-register
// the moment it is transferred, which is what turns that into a grant.
//
// A WRITE CANNOT BE UNDONE BY CHECKING AFTERWARDS, which is why the refusal is
// before the second request rather than a re-verification after it: by the time
// a re-fetch could notice, the hook is installed and the repository is gone.
// 301 makes it worse, not better — Go rewrites a non-HEAD request to GET on
// 301/302/303, so a Delete would have followed the redirect, read a 200 and
// reported success having deleted nothing.
//
// It is one rule on the client rather than a check per call because it must hold
// for every request this driver makes, and because a rule with no host arithmetic in it cannot disagree
// with the pin. ErrUseLastResponse hands the 3xx to send, which treats it as the
// non-2xx it is: one error path, and the status is the fact an operator needs.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (c *client) Host() string { return c.d.host }

// statusError is a non-2xx answer, carrying the request this driver made and
// the code it got back — and nothing the host chose. Every mapping into the
// forge taxonomy reads it and only it.
type statusError struct {
	Method string
	Path   string
	Status int
}

// Refused is a 4xx: the forge's own answer refusing the request.
func (e *statusError) Refused() bool { return e.Status >= 400 && e.Status < 500 }

func (e *statusError) Error() string {
	return fmt.Sprintf("forge/gogs: %s %s answered %d %s", e.Method, e.Path, e.Status, http.StatusText(e.Status))
}

// send issues one request against the API base and decodes a 2xx body into out.
//
// IT TAKES A ROUTE AND NOT A PATH, and that is the whole of why there is a
// route type: this is the only place in the package that builds an
// *http.Request, so a path that reaches the wire is a path some route composed,
// with every segment escaped by the one function that escapes segments. A call
// site cannot spell a path of its own without declaring the route it belongs
// to, and a declared route the sweep never reaches fails the sweep.
//
// A non-2xx becomes a *statusError; a transport failure stays whatever it was,
// wrapped, so that an outage can never be pattern-matched into a verdict.
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

// classify maps a failure about ONE REPOSITORY into the taxonomy.
//
//	401  the credential was refused. A statement about the credential, never a
//	     verdict on the repository: a consumer that read it as one would deny a
//	     legitimate writer whose token merely lapsed.
//	404  the host serves no such repository — which on Gogs also covers a
//	     private repository this credential may not see, and those two are
//	     indistinguishable from outside on every forge.
//
// EVERYTHING ELSE STAYS AN ERROR AND IS NOT A VERDICT. A 403 in particular is
// not "you may not push": on Gogs it is a token missing a scope, a disabled
// account, or a reverse proxy's policy — an operator problem, not an answer about
// this caller.
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

// access maps Gogs's three flags onto tau's four rungs.
//
// push|admin is write. What matters is the rung below it: PULL IS AccessRead AND NOT AccessWrite. A public
// repository returns pull:true to every token on earth, and reading that as
// permission is what let an unrelated caller reach another project's
// repositories.
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

// repository turns Gogs's answer into forge's, deriving the key from the
// CLONE URL the forge returned rather than echoing the argument. That is what
// makes a rename, a redirect or a case-folding disagreement visible instead of
// silently absorbed: a caller that echoed its own argument would never know.
//
// asked IS WHAT TAU CALLED THIS REPOSITORY, and every error below names it
// rather than the full_name the forge sent, and carries no reason repokey gave.
// The forge chose both of those strings while holding the caller credential,
// and repokey QUOTES WHAT IT WAS GIVEN — so a clone URL with an identity half,
// which is the one shape repokey calls a secret at rest, was the forge writing
// a credential of its choosing into tau logs through the one path that refuses
// them. Measured: a 200 answering
// clone_url https://x-access-token:<token>@host/acme/pay.git put that token in
// the error. A caller always has a name of its own — the key it asked about, or
// the coordinates of the listing entry — and it is the more useful half of the
// mismatch anyway. This is the gitlab driver rule, and two drivers over one
// interface may not disagree about a threat the layer has written down.
//
// A CLONE URL ON ANOTHER HOST IS REFUSED, and the gitlab driver refuses the same
// thing for the same reason — it calls it not hypothetical, because an instance
// behind a proxy that was told the wrong external URL emits exactly that. Two
// things go wrong when it is accepted. The key derived from it files the
// repository under a host this deployment has no driver for, which is a
// repository nothing can ever act on again; and HTTPS below is the address a
// builder clones with a credential, so a host that gets to name it gets to name
// where that credential is spent — 169.254.169.254 included.
//
// The SSH URL is passed through unchecked, deliberately and for the reason the
// gitlab driver states: a forge may legitimately serve SSH on another name or
// port, which is a good clone address and a different repokey key. Nothing
// clones it with a bearer credential, so it is a fact reported rather than a
// place a secret goes.
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
