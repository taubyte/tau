// Package onedev is the forge driver for OneDev (onedev.io), the self-hosted
// git server whose REST API lives under /~api.
//
// # OneDev is not shaped like GitHub, and every difference is a place to be wrong
//
//   - PROJECTS NEST, ARBITRARILY DEEP, AND THERE ARE NO USER NAMESPACES. A
//     project's path is its ancestors' names and its own, "acme/team/widget",
//     so a key's Owner is the parent project's path and may hold any number of
//     "/". The driver addresses that depth rather than refusing it, and it
//     refuses the one shape OneDev has that a key cannot spell: a root project,
//     whose path has no owner half at all.
//
//   - EVERYTHING IS ADDRESSED BY A NUMBER. Every endpoint but one takes the
//     project's numeric id, so each operation starts by resolving the path the
//     caller named (GET /~api/projects/ids/{path}) and then speaks about the id
//     it was given. The id never leaves the package; a key is still derived
//     from the clone URL the forge answers with, so a project that answers for
//     a path it no longer has is reported under the path it does.
//
//   - THERE IS NO "MY PERMISSION" ENDPOINT. OneDev answers each privilege by
//     refusing what the credential may not do, so Access asks three things in
//     order and reads the first yes: may it read the project's settings (manage
//     the project: admin), may it open a git-receive-pack advertisement (push:
//     write), may it read the default branch (read code: read). A 403 from one
//     of those is OneDev's own definitive "no" to an authenticated caller — the
//     id lookup that precedes them has already proved the credential is good —
//     and a project the credential reaches without code access is AccessNone,
//     which is what it is for a builder.
//
//   - THERE ARE NO PUSH HOOKS, BECAUSE ONEDEV CANNOT SIGN ONE. OneDev puts a
//     hook's stored secret, verbatim, in a header it calls X-OneDev-Signature,
//     and computes nothing over the body, so anyone who sees one delivery's
//     headers can forge any push. tau accepts no delivery it cannot verify by a
//     signature over the body, so this driver implements neither
//     forge.PushHooks nor forge.Signatures and both answer
//     *forge.UnsupportedError. A OneDev repository is reachable through every
//     other face; its pushes do not trigger a build.
//
//   - THERE ARE NO DEPLOY KEYS AND NO NAMESPACE MEMBERSHIP. An SSH key in
//     OneDev belongs to a user and reaches everything that user reaches, so
//     "install a read-only key on one repository" has no OneDev spelling; and a
//     parent project is not an organisation with members. Both faces are absent
//     and answer *forge.UnsupportedError.
//
// No SDK: there is none, and the whole of what tau asks is the routes in
// route.go.
package onedev

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// driver is one OneDev, constructed for the host it answers for. It carries no
// credential at all: OneDev has nothing a deployment credential could answer
// that a caller's could not, so there is no second driver type.
type driver struct {
	host string
	// base is the server root, absolute and ending in "/". The REST API is
	// under /~api/ and git under the project paths, both on this one host.
	base *url.URL

	rt     http.RoundTripper // under the pin; nil means http.DefaultTransport
	scheme string            // "https", or "http" only for a loopback host
}

var _ forge.Driver = (*driver)(nil)

// Option is a deployment fact about one OneDev. There is no option for the API
// base: the host decides it.
type Option func(*driver) error

// Transport is the RoundTripper UNDER the credential pin — a private CA, a
// proxy, a test server. PinnedTransport wraps it and checks every hop.
func Transport(rt http.RoundTripper) Option {
	return func(d *driver) error {
		if rt == nil {
			return errors.New("forge/onedev: Transport was given no RoundTripper")
		}
		d.rt = rt
		return nil
	}
}

// Plaintext lets the driver speak http:// to a LOOPBACK host, and only to one.
// It exists for the container suite; PinnedTransport refuses a bearer header
// over http to any other host whatever this says.
func Plaintext() Option {
	return func(d *driver) error {
		d.scheme = "http"
		return nil
	}
}

// New builds the driver for one OneDev host.
func New(host string, opts ...Option) (forge.Driver, error) {
	if host == "" {
		return nil, errors.New("forge/onedev: a driver names the host it answers for")
	}
	if k, err := repokey.Parse("https://" + host + "/owner/name"); err != nil || k.Host != host {
		return nil, fmt.Errorf("forge/onedev: %q is not a host spelling repokey produces (lowercase, and a port only when it is not the transport's default)", host)
	}
	d := &driver{host: host, scheme: "https"}
	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	d.base = &url.URL{Scheme: d.scheme, Host: host, Path: "/"}
	return d, nil
}

func (d *driver) Host() string { return d.host }

// Addressable refuses what OneDev cannot address. A nested owner is a parent
// project's path and is addressed; a key with no owner would be a root
// project, which a repokey key cannot name.
func (d *driver) Addressable(key repokey.Key) error {
	if key.Host != d.host {
		return fmt.Errorf("this driver answers for %s", d.host)
	}
	if key.Owner == "" {
		return errors.New("OneDev serves a repository under a parent project, and this key names none")
	}
	for _, seg := range strings.Split(key.FullName(), "/") {
		if err := addressesItself(seg); err != nil {
			return err
		}
	}
	return nil
}

// Bind returns a client acting as cred and only as cred.
func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}
