// Package sourcehut is the forge driver for git.sr.ht: sr.ht itself and any
// self-hosted sourcehut, through git.sr.ht's GraphQL API at /query.
//
// # sourcehut is not shaped like GitHub, and the differences are the driver
//
//   - THERE ARE NO ORGANIZATIONS. Every repository belongs to one user and is
//     addressed ~user/name, so a key's owner carries the tilde and a nested
//     owner is refused before the wire. A repository is created only under the
//     credential's own account, and membership — "does this user belong to
//     this organization" — has nothing to ask, so CapMembership is absent and
//     answers *forge.UnsupportedError rather than a false.
//
//   - THE API IS ONE ENDPOINT. Every operation is a POST of a named GraphQL
//     document to /query; a redirect is never followed, so a same-host hop
//     cannot pick which repository a question was about.
//
//   - EVERYTHING WRITES BY INTERNAL ID. deleteRepository, createDeployKey,
//     createGitWebhook take sourcehut's own ids, so each write first resolves
//     the key to its repository, and every answer that names a repository is
//     checked against the key that was asked about. Removing a hook or a deploy
//     key by a handle first lists that repository's own hooks or keys, because
//     the handle is global on sourcehut and a removal that trusted it could
//     remove a resource on a repository nobody named.
//
//   - A DEPLOY KEY HAS NO TITLE. sourcehut keeps the key's comment, so the
//     title is written as the comment of the key installed and read back from
//     it. The mode is RO, and a key the forge reports as anything else is
//     removed again and the install refused.
//
//   - DELIVERIES ARE SIGNED WITH THE INSTANCE'S OWN ED25519 KEY, not a shared
//     secret: forge.SchemePayloadEd25519, which says why a per-hook secret is
//     still needed and how this driver spends it (an alias in the query it
//     registers). The key is a deployment fact: sr.ht's published one is the
//     default for git.sr.ht, and a self-hosted instance names its own with
//     PayloadKey. A driver with no key cannot verify, so it has no Signatures
//     face and RegisterPushHook answers *forge.UnsupportedError.
//
//   - NO PUSH PROBE: answering it means speaking git over SSH, which no driver
//     here does yet.
//
// No SDK and no GraphQL library: the whole of what tau asks is a dozen fixed
// documents, and a client generator would put its types one careless signature
// away from this boundary.
package sourcehut

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// PublicHost is sr.ht's own git service.
const PublicHost = "git.sr.ht"

// srhtPayloadKey is the key sr.ht signs every webhook delivery with, as
// published at https://man.sr.ht/graphql.md#webhooks.
const srhtPayloadKey = "uX7KWyyDNMaBma4aVbJ/cbUQpdjqczuCyK/HxzV/u+4="

// driver is one git.sr.ht, constructed for the host it answers for. It carries
// no caller credential.
type driver struct {
	host string
	// endpoint is the GraphQL endpoint. git.sr.ht serves it from /query on
	// the host the repositories are on, so the API host and the repository
	// host are one value here.
	endpoint *url.URL

	rt      http.RoundTripper // under the pin; nil means http.DefaultTransport
	scheme  string            // "https", or "http" only for a loopback host
	sshUser string
	key     ed25519.PublicKey
}

// signingDriver is a driver that knows the key its forge signs with, and so can
// read deliveries. It is a separate type because forge computes capabilities by
// type assertion and whether a key is known is decided once, at New.
type signingDriver struct{ *driver }

var (
	_ forge.Driver     = (*driver)(nil)
	_ forge.Driver     = (*signingDriver)(nil)
	_ forge.Signatures = (*signingDriver)(nil)
	_ forge.PayloadKey = (*signingDriver)(nil)
)

// Option is a deployment fact about one sourcehut.
type Option func(*driver) error

// Transport is the RoundTripper UNDER the credential pin.
func Transport(rt http.RoundTripper) Option {
	return func(d *driver) error {
		if rt == nil {
			return errors.New("forge/sourcehut: Transport was given no RoundTripper")
		}
		d.rt = rt
		return nil
	}
}

// PayloadKey names the Ed25519 key this instance signs webhook deliveries
// with, std base64 as sourcehut publishes it (an instance's api-meta.json).
func PayloadKey(b64 string) Option {
	return func(d *driver) error {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return errors.New("forge/sourcehut: a payload key is the std base64 of a 32-byte Ed25519 public key")
		}
		d.key = ed25519.PublicKey(raw)
		return nil
	}
}

// SSHUser is the account before the '@' of an SSH clone address, the
// instance's [git.sr.ht] ssh-user. It is "git" unless an operator changed it.
func SSHUser(user string) Option {
	return func(d *driver) error {
		if user == "" || strings.ContainsAny(user, "@:/ ") {
			return errors.New("forge/sourcehut: an SSH user is one account name")
		}
		d.sshUser = user
		return nil
	}
}

// Plaintext lets the driver speak http:// to a LOOPBACK host, and only to one.
// PinnedTransport refuses a bearer header over http to any other host.
func Plaintext() Option {
	return func(d *driver) error {
		d.scheme = "http"
		return nil
	}
}

// New builds the driver for one git.sr.ht host.
func New(host string, opts ...Option) (forge.Driver, error) {
	if host == "" {
		return nil, errors.New("forge/sourcehut: a driver names the host it answers for")
	}
	if k, err := repokey.Parse("https://" + host + "/~owner/name"); err != nil || k.Host != host {
		return nil, fmt.Errorf("forge/sourcehut: %q is not a host spelling repokey produces (lowercase, and a port only when it is not the transport's default)", host)
	}
	d := &driver{host: host, scheme: "https", sshUser: "git"}
	if host == PublicHost {
		raw, err := base64.StdEncoding.DecodeString(srhtPayloadKey)
		if err != nil {
			return nil, err
		}
		d.key = raw
	}
	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	d.endpoint = &url.URL{Scheme: d.scheme, Host: host, Path: "/query"}
	if d.key != nil {
		return &signingDriver{d}, nil
	}
	return d, nil
}

func (d *driver) Host() string { return d.host }

// Addressable refuses what sourcehut cannot address: exactly one ~user and one
// name.
func (d *driver) Addressable(key repokey.Key) error {
	if key.Host != d.host {
		return fmt.Errorf("this driver answers for %s", d.host)
	}
	if strings.Contains(key.Owner, "/") {
		return errors.New("sourcehut addresses exactly one ~user and one name, and this key nests")
	}
	if len(key.Owner) < 2 || key.Owner[0] != '~' {
		return errors.New("sourcehut serves every repository under a ~user, and this key names none")
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

// PayloadKey is the key this instance signs deliveries with.
func (d *signingDriver) PayloadKey() ed25519.PublicKey { return d.key }
