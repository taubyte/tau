package forge

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// Every question has three outcomes: yes, no, and "could not ask". The third is
// always an error, never a zero value dressed as an answer.

var (
	// ErrNoCredential is a zero Credential. There is no implicit anonymity;
	// Anonymous() exists so that saying "nobody" is a thing a call site does
	// out loud.
	ErrNoCredential = errors.New("forge: no credential; pass a token, or Anonymous() to say so out loud")

	// ErrAnonymous is an authorization question asked of a session bound to no
	// principal.
	ErrAnonymous = errors.New("forge: an anonymous session cannot answer an authorization question")

	ErrNotAPush = errors.New("forge: delivery is not a push")

	// ErrRefDeleted is a push that deletes a ref. It is separate from
	// ErrNotAPush because it is a legitimate delivery with nothing to build: a
	// receiver answers 200 and drops it, rather than logging a failure it will
	// then chase.
	ErrRefDeleted = errors.New("forge: push deletes the ref")

	ErrSecretNotSerializable = errors.New("forge: a Secret cannot be serialized; call Expose")
)

// NoDriverError is not a fallback point: there is no generic driver, since one
// answering "may this caller write" by shrugging is worse than none.
type NoDriverError struct{ Host string }

func (e *NoDriverError) Error() string { return fmt.Sprintf("forge: no driver for host %q", e.Host) }

// UnsupportedError is a forge that exists and cannot be asked THIS question — a
// webhook on a host with no webhooks, membership on a forge with no
// deployment-credential concept. It is never a false and never a zero value.
type UnsupportedError struct {
	Host       string
	Capability Capability
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("forge: %s cannot answer %s", e.Host, e.Capability)
}

// UnaddressableError is a key whose shape this forge cannot address. A driver
// never joins segments into a name no repository has.
type UnaddressableError struct {
	Host   string
	Key    repokey.Key
	Reason string
}

func (e *UnaddressableError) Error() string {
	return fmt.Sprintf("forge: %s cannot address %q: %s", e.Host, e.Key, e.Reason)
}

// WrongHostError is a key handed to a session bound to a different host. The
// session holds a credential for its own host, so acting on the key would mean
// either spending that credential elsewhere or answering about a repository
// nobody asked about.
type WrongHostError struct {
	Session string
	Key     repokey.Key
}

func (e *WrongHostError) Error() string {
	return fmt.Sprintf("forge: session is bound to %s and cannot act on %q", e.Session, e.Key)
}

// NotFoundError: a host hides a repository the credential may not read, so "no
// such repository" and "you may not see it" are one answer.
type NotFoundError struct {
	Host string
	Key  repokey.Key
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("forge: %s serves no repository %q", e.Host, e.Key)
}

// CredentialError is the credential refused (expired, revoked, wrong scope),
// not a verdict on the repository. Reason must not quote the credential.
type CredentialError struct {
	Host   string
	Reason string
}

func (e *CredentialError) Error() string {
	return fmt.Sprintf("forge: %s refused the credential: %s", e.Host, e.Reason)
}

// VerificationError CARRIES THE SCHEME AND A REASON AND NEVER THE EVIDENCE. No
// expected MAC, no presented MAC, no secret, no body. An operator needs to know
// which check failed; nothing needs the value that failed it.
type VerificationError struct {
	Scheme Scheme
	Reason string
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("forge: delivery failed %s verification: %s", e.Scheme, e.Reason)
}

// CredentialLeakError is a request that would have carried a credential off the
// host it was bound to. See PinnedTransport.
type CredentialLeakError struct {
	Bound string
	To    string
}

func (e *CredentialLeakError) Error() string {
	return fmt.Sprintf("forge: refusing to carry a %s credential to %s", e.Bound, e.To)
}

// ExistsError is a creation refused because a repository is already at the
// key it named. It is said only when the forge, asked after refusing, shows
// that repository to the credential; a name taken by something the caller
// cannot see stays the forge's own refusal.
type ExistsError struct {
	Host string
	Key  repokey.Key
}

func (e *ExistsError) Error() string {
	return fmt.Sprintf("forge: %s already has a repository %q", e.Host, e.Key)
}
