package forge

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"log/slog"
)

// SecretSize is the length of every secret this package mints. One generator,
// one length: v1 has two, and one of them is the literal "mock-secret" reachable
// from a dev flag.
const SecretSize = 32

// Secret is key material that closes EVERY way of rendering a value, not just
// String. `%v` on a struct holding an unexported []byte prints the bytes, `%x`
// prints hex, a json.Marshal of an enclosing response prints base64, and a
// structured logger reaches unexported fields by reflection. So Secret
// implements fmt.Formatter (which catches every verb), fmt.Stringer, the JSON
// and text and binary marshallers, and slog.LogValuer.
//
// THE MARSHALLERS FAIL RATHER THAN REDACT. A redacted secret written into a
// store is a secret that is gone, and learning that at the next verification is
// worse than learning it at the write.
//
// Key material leaves by ONE door, Expose, which has a name a reviewer can grep
// for.
type Secret struct{ b []byte }

// NewSecret mints a secret. It is the only generator in this package.
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

// tooShort is the STRENGTH floor, and SecretSize is both the mint and the
// floor because there is no honest second number.
//
// A Secret keys an HMAC-SHA256. RFC 2104 §3 says a key shorter than the hash's
// own output is "strongly discouraged" because the construction is then no
// stronger than the key, and NIST SP 800-107 §5.3.4 says the same in the other
// direction: the key must carry at least the security strength being claimed.
// For SHA-256 that is 32 bytes, which is what NewSecret already mints and what
// SchemeStandardWebhooks already demanded -- as a side effect of GitLab's
// whsec_ FORMAT, not for any reason about strength, which is why the other
// three schemes happily keyed a MAC with two base64 characters.
//
// Length is a proxy for entropy and it is the only proxy anything here can
// check: nothing can tell 32 random bytes from 32 zeros. This is a floor under
// the accident -- a short string out of a config file, a reused word -- and not
// a certificate that the bytes above it are any good.
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

// Credential is one principal's proof, held by a Session and spent on every
// call it makes. It is bound once rather than passed per call because the
// credential kinds this layer meets have different lifetimes — a caller's
// request-scoped token, the deployment's process-scoped machine credential, a
// per-repository key, a per-hook secret — and a `token string` parameter on
// every method models exactly one of them and forces the rest into package
// variables.
type Credential struct {
	Secret
	anonymous bool
}

// Token is a caller's forge token — a PAT, an OAuth token, an installation
// token. How it becomes an Authorization header is the driver's: GitHub wants
// Bearer, Gitea wants "token <t>", GitLab accepts PRIVATE-TOKEN.
func Token(t string) Credential { return Credential{Secret: SecretFrom([]byte(t))} }

// Anonymous is a credential that is nothing, WRITTEN DOWN AT THE CALL SITE so a
// reviewer sees it. A session bound to it refuses every authorization question,
// because "what may nobody in particular do here" is not an answer anyone may
// act on.
func Anonymous() Credential { return Credential{anonymous: true} }

func (c Credential) IsAnonymous() bool { return c.anonymous }

// IsZero is a credential nobody set. Binding one is ErrNoCredential, never
// silent anonymity: v1 returns an unauthenticated client for an empty token, so
// a dropped token there degrades into a client answering with a stranger's view
// of the world.
func (c Credential) IsZero() bool { return !c.anonymous && c.Empty() }
