package forge

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// The failure taxonomy IS the security contract of this package. Every question
// has three outcomes and not two: yes, no, and "I could not ask". Only the first
// two are values. The third is always an error, and never a zero value dressed
// as an answer.
//
// v1 already fixed this contract for one question — a 404 is the only definitive
// non-member, "because a 403 can mean the app was blocked, which is an operator
// problem, not a verdict on the user" — and everything below is that same rule
// stated once for every question this package asks.

var (
	// ErrNoCredential is a zero Credential. There is no implicit anonymity;
	// Anonymous() exists so that saying "nobody" is a thing a call site does
	// out loud.
	ErrNoCredential = errors.New("forge: no credential; pass a token, or Anonymous() to say so out loud")

	// ErrAnonymous is an authorization question asked of a session bound to no
	// principal.
	ErrAnonymous = errors.New("forge: an anonymous session cannot answer an authorization question")

	// ErrNotAPush is a verified delivery that is not a push event.
	ErrNotAPush = errors.New("forge: delivery is not a push")

	// ErrRefDeleted is a push that deletes a ref. It is separate from
	// ErrNotAPush because it is a legitimate delivery with nothing to build: a
	// receiver answers 200 and drops it, rather than logging a failure it will
	// then chase.
	ErrRefDeleted = errors.New("forge: push deletes the ref")

	// ErrSecretNotSerializable is what every marshaller on Secret returns.
	ErrSecretNotSerializable = errors.New("forge: a Secret cannot be serialized; call Expose")
)

// NoDriverError is a host this deployment has no driver for. IT IS NOT A
// FALLBACK POINT: there is no generic driver behind it, because a driver that
// answers "may this caller write" by shrugging is worse than no answer. What a
// consumer does with it is a policy the consumer writes down, in its own code,
// where a reviewer finds it.
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

// UnaddressableError is a key whose SHAPE this forge cannot address — a GitLab
// subgroup handed to a driver whose API takes exactly one owner and one name. A
// driver refuses; it never joins the segments into a name no repository has,
// because that is how a request ends up addressing something other than what
// the caller named. Raised by the registry before any request leaves the
// process.
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

// NotFoundError is the host saying it serves no such repository. IT IS NOT A
// REFUSAL, and the ambiguity is documented rather than papered over: a host
// hides a repository the credential may not read, so "no such repository" and
// "you may not see it" are indistinguishable from outside. A consumer that
// needs them separated cannot get that from any forge.
type NotFoundError struct {
	Host string
	Key  repokey.Key
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("forge: %s serves no repository %q", e.Host, e.Key)
}

// CredentialError is the credential being refused — expired, revoked, wrong
// scope. It is not a verdict about the repository, and a consumer that reads it
// as one denies a legitimate writer whose token merely lapsed.
//
// Reason is a driver's own words and MUST NOT quote the credential: an error
// string is the most-copied thing in a log.
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
