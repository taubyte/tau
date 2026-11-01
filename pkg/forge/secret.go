package forge

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"log/slog"
)

// SecretSize is the length of every secret this package mints.
const SecretSize = 32

// Secret is key material that closes every way of rendering a value: it
// implements fmt.Formatter (every verb), fmt.Stringer, the JSON, text and binary
// marshallers, and slog.LogValuer. The marshallers fail rather than redact: a
// redacted secret written into a store is gone. Key material leaves only by
// Expose.
type Secret struct{ b []byte }

func NewSecret() (Secret, error) {
	b := make([]byte, SecretSize)
	if _, err := rand.Read(b); err != nil {
		return Secret{}, fmt.Errorf("forge: generating a secret: %w", err)
	}
	return Secret{b: b}, nil
}

// SecretFrom wraps bytes a store handed back. It copies, so the caller's slice
// is not aliased into something that will outlive it.
func SecretFrom(b []byte) Secret {
	if len(b) == 0 {
		return Secret{}
	}
	return Secret{b: append([]byte(nil), b...)}
}

// Expose is the only door. Every call site is a place to ask whether the value
// is about to be written somewhere it should not be.
func (s Secret) Expose() []byte { return s.b }

func (s Secret) Empty() bool { return len(s.b) == 0 }

// tooShort is the strength floor, SecretSize: an HMAC-SHA256 key shorter than
// the hash's output is no stronger than the key (RFC 2104 §3, NIST SP 800-107
// §5.3.4). Length is the only proxy for entropy anything here can check; this
// catches the accident, a short config string, not bad bytes.
func (s Secret) tooShort() bool { return len(s.b) < SecretSize }

// Equal compares in constant time. Two empty secrets are NOT equal: "no secret
// configured" must never compare equal to anything, least of all to another
// unconfigured hook.
func (s Secret) Equal(other Secret) bool {
	if s.Empty() || other.Empty() {
		return false
	}
	return subtle.ConstantTimeCompare(s.b, other.b) == 1
}

func (s Secret) String() string { return "forge.Secret(redacted)" }

// Format catches every verb, including the ones Stringer does not: %x, %q, %#v
// and the bare %v on an enclosing struct.
func (s Secret) Format(f fmt.State, verb rune) { fmt.Fprint(f, s.String()) }

func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

func (s Secret) MarshalJSON() ([]byte, error)   { return nil, ErrSecretNotSerializable }
func (s Secret) MarshalText() ([]byte, error)   { return nil, ErrSecretNotSerializable }
func (s Secret) MarshalBinary() ([]byte, error) { return nil, ErrSecretNotSerializable }

// Credential is one principal's proof, bound once to a Session rather than
// passed per call: the kinds this layer meets (a request-scoped token, the
// deployment's machine credential, a per-hook secret) have different lifetimes.
type Credential struct {
	Secret
	anonymous bool
}

// Token is a caller's forge token — a PAT, an OAuth token, an installation
// token. How it becomes an Authorization header is the driver's: GitHub wants
// Bearer, Gitea wants "token <t>", GitLab accepts PRIVATE-TOKEN.
func Token(t string) Credential { return Credential{Secret: SecretFrom([]byte(t))} }

// Anonymous is a credential that is nothing, written down at the call site. A
// session bound to it refuses every authorization question.
func Anonymous() Credential { return Credential{anonymous: true} }

func (c Credential) IsAnonymous() bool { return c.anonymous }

// IsZero is a credential nobody set; binding one is ErrNoCredential, never
// silent anonymity.
func (c Credential) IsZero() bool { return !c.anonymous && c.Empty() }
