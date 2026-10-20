// Package bitbucketserver is the forge driver for Bitbucket Server and Data
// Center, which speak /rest/api/1.0 on a host of their own. It is not
// Bitbucket Cloud: that is api.bitbucket.org/2.0, other nouns, and
// pkg/forge/bitbucket.
//
// # One repository, two addresses, one key
//
// A repository is a project and a slug, and Bitbucket serves its clones at
//
//	https://{host}/scm/{PROJECT}/{slug}.git
//	ssh://git@{host}:7999/{PROJECT}/{slug}.git
//
// which repokey, knowing no forge, reads as two repositories — one under an
// extra "scm" segment, one on another port. The driver's forge.Spellings says
// what both are: the key {host}/{project}/{slug}, lowercased like every key. The
// registry binds the ssh host:port to this driver for spelling alone, so every
// consumer keying an address through forge.Registry.Key files both addresses,
// and every push, under one key. A personal repository's project is "~{user}".
//
// Bitbucket stores a project key in upper case and a key is lowercase, so a
// request upper-cases it back; a personal project's "~{user}" is a user slug,
// already lowercase. A server under a context path (https://host/bitbucket/)
// spells its paths differently and is not addressed.
//
// # Where Bitbucket Server differs
//
//   - PERMISSION IS A FILTER, NOT A FIELD. A repository answer says nothing
//     about what the caller may do to it; /repos?permission=REPO_ADMIN lists
//     the repositories the caller administers. A visible repository in neither
//     the admin nor the write listing is AccessRead — VISIBILITY IS NOT
//     PERMISSION.
//   - A 401 IS TWO THINGS. Bitbucket answers both a refused credential and an
//     authenticated caller lacking a permission with 401, and tells them apart
//     only by the exception it names. An AuthorisationException is a refusal
//     about the repository, not about the credential, and is returned as what
//     it is rather than as a *forge.CredentialError.
//   - A DEPLOY KEY IS AN ACCESS KEY under /rest/keys/1.0, and it carries its
//     permission. This driver sends REPO_READ and nothing else.
//   - A PUSH IS repo:refs_changed, signed X-Hub-Signature with "sha256=" and
//     lowercase hex — forge.SchemeBitbucketSHA256, verified by the core. The
//     delivery names its repository by project key and slug.
//
// # What it cannot answer
//
//   - MEMBERSHIP. A project's members are permissions granted to users and
//     groups, read only by a project administrator; a deployment credential
//     that is not one reads every non-member as "cannot look", which the
//     Memberships contract forbids reading as "not a member".
//   - A PUSH PROBE. The driver speaks REST, not git.
//
// No SDK: what tau asks Bitbucket is the requests in route.go.
package bitbucketserver

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const (
	// defaultSSHPort is the port Bitbucket Server serves ssh on unless an
	// administrator changed it.
	defaultSSHPort = 7999

	// scmSegment is the path segment Bitbucket serves https clones under.
	scmSegment = "scm"
)

// driver is one Bitbucket Server, constructed for the host it answers for. It
// carries no caller credential.
type driver struct {
	host string
	// sshHost is the repokey host the server's ssh addresses are on: the same
	// host name, on the ssh port.
	sshHost string
	sshPort int
	// base is the host's root, absolute and ending in "/"; every route is a
	// path under it.
	base *url.URL
	rt   http.RoundTripper
}

var (
	_ forge.Driver     = (*driver)(nil)
	_ forge.Signatures = (*driver)(nil)
	_ forge.Spellings  = (*driver)(nil)
)

// Option is a deployment fact about one Bitbucket Server.
type Option func(*driver) error

// Transport is the RoundTripper UNDER the credential pin.
func Transport(rt http.RoundTripper) Option {
	return func(d *driver) error {
		if rt == nil {
			return errors.New("forge/bitbucketserver: Transport was given no RoundTripper")
		}
		d.rt = rt
		return nil
	}
}

// SSHPort is the port the server serves ssh on, when an administrator moved
// it from 7999.
func SSHPort(port int) Option {
	return func(d *driver) error {
		if port < 1 || port > 65535 {
			return fmt.Errorf("forge/bitbucketserver: %d is not a port", port)
		}
		d.sshPort = port
		return nil
	}
}

// New builds the driver for one host.
func New(host string, opts ...Option) (forge.Driver, error) {
	if host == "" {
		return nil, errors.New("forge/bitbucketserver: a driver names the host it answers for")
	}
	if k, err := repokey.Parse("https://" + host + "/owner/name"); err != nil || k.Host != host {
		return nil, fmt.Errorf("forge/bitbucketserver: %q is not a host spelling repokey produces (lowercase, and a port only when it is not the transport's default)", host)
	}
	d := &driver{host: host, sshPort: defaultSSHPort}
	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	// The ssh host is the same name on the ssh port, spelled the way repokey
	// spells it, so the port drops when it is ssh's own.
	name := (&url.URL{Host: host}).Hostname()
	if strings.Contains(name, ":") {
		name = "[" + name + "]"
	}
	k, err := repokey.Parse("ssh://git@" + name + ":" + strconv.Itoa(d.sshPort) + "/owner/name")
	if err != nil {
		return nil, fmt.Errorf("forge/bitbucketserver: %q serves no ssh address repokey can read: %w", host, err)
	}
	d.sshHost = k.Host
	d.base = &url.URL{Scheme: "https", Host: host, Path: "/"}
	return d, nil
}

func (d *driver) Host() string { return d.host }

// SpellingHosts is the ssh host:port, unless ssh is on the https host's own
// spelling, which is bound already.
func (d *driver) SpellingHosts() []string {
	if d.sshHost == d.host {
		return nil
	}
	return []string{d.sshHost}
}

// Canonical files both of Bitbucket's addresses under {host}/{project}/{slug}:
// the https one by dropping its "scm" segment, the ssh one by moving it to the
// host. A key already canonical maps to itself; anything else is not a
// spelling this driver rewrites.
func (d *driver) Canonical(k repokey.Key) (repokey.Key, bool) {
	switch k.Host {
	case d.host:
		project, ok := strings.CutPrefix(k.Owner, scmSegment+"/")
		if !ok || project == "" || strings.Contains(project, "/") {
			return k, false
		}
		return repokey.Key{Host: d.host, Owner: project, Name: k.Name}, true
	case d.sshHost:
		if k.Owner == "" || strings.Contains(k.Owner, "/") {
			return k, false
		}
		return repokey.Key{Host: d.host, Owner: k.Owner, Name: k.Name}, true
	}
	return k, false
}

// Addressable refuses what Bitbucket cannot address: exactly one project and
// one slug, so a nested key is refused rather than flattened.
func (d *driver) Addressable(key repokey.Key) error {
	_, err := d.coords(key)
	return err
}

// coords are the two names a request addresses a repository by, spelled the
// way Bitbucket stores them.
type coords struct{ project, slug string }

func (d *driver) coords(key repokey.Key) (coords, error) {
	switch {
	case key.Host != d.host:
		return coords{}, fmt.Errorf("this driver answers for %s", d.host)
	case key.Owner == "":
		return coords{}, errors.New("Bitbucket Server serves every repository under a project, and this key names none")
	case strings.Contains(key.Owner, "/"):
		return coords{}, errors.New("Bitbucket Server addresses exactly one project and one repository, and this key nests")
	}
	return coords{project: projectKey(key.Owner), slug: key.Name}, nil
}

// projectKey is a key's owner as Bitbucket stores it: a project key in upper
// case, a personal project's "~{user}" as the user slug it is.
func projectKey(owner string) string {
	if strings.HasPrefix(owner, "~") {
		return owner
	}
	return strings.ToUpper(owner)
}

// keyOf is the key of the repository Bitbucket names by project key and slug.
func (d *driver) keyOf(project, slug string) (repokey.Key, error) {
	if project == "" || slug == "" {
		return repokey.Key{}, errors.New("names no project or no slug")
	}
	escaped, err := escapedSegments([]string{project, slug})
	if err != nil {
		return repokey.Key{}, err
	}
	k, err := repokey.Parse("https://" + d.host + "/" + escaped)
	if err != nil || k.Host != d.host || strings.Contains(k.Owner, "/") {
		return repokey.Key{}, errors.New("names a repository this layer cannot derive a key for")
	}
	return k, nil
}

// Bind returns a client acting as cred and only as cred.
func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}
