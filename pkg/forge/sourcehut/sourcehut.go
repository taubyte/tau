// Package sourcehut is the forge driver for git.sr.ht, sr.ht or self-hosted,
// through its GraphQL API at /query.
//
// There are no organizations: a repository is ~user/name, created only under
// the credential's own account, and membership is absent. Every operation is a
// POST of a named document to one endpoint, and no redirect is followed. Writes
// take internal ids, so each resolves the key first and checks the answer; a
// hook or deploy key handle is global, so removal lists the repository's own
// first. A deploy key has no title, so the title is the key's comment. Deliveries
// are signed with the instance's Ed25519 key (forge.SchemePayloadEd25519); a
// driver with no key has no Signatures face. No push probe.
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

const PublicHost = "git.sr.ht"

// srhtPayloadKey is the key sr.ht signs every webhook delivery with, as
// published at https://man.sr.ht/graphql.md#webhooks.
const srhtPayloadKey = "uX7KWyyDNMaBma4aVbJ/cbUQpdjqczuCyK/HxzV/u+4="

// driver carries no caller credential.
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

// signingDriver knows the key its forge signs with, as a separate type:
// capabilities come from type assertions, and whether a key is known is decided
// at New.
type signingDriver struct{ *driver }

var (
	_ forge.Driver     = (*driver)(nil)
	_ forge.Driver     = (*signingDriver)(nil)
	_ forge.Signatures = (*signingDriver)(nil)
	_ forge.PayloadKey = (*signingDriver)(nil)
)

type Option func(*driver) error

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

func (d *driver) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return d.bind(string(cred.Expose())), nil
}

func (d *signingDriver) PayloadKey() ed25519.PublicKey { return d.key }
