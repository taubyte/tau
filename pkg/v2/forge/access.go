package forge

import "github.com/taubyte/tau/pkg/git/repokey"

// Access is what a principal may do to one repository, IN TAU'S VOCABULARY.
// The rungs are chosen by the questions tau asks, not by any forge's role
// names: GitHub has five roles, GitLab eight integer levels, Gitea three
// booleans and Bitbucket three strings, and each driver maps its own onto
// these. Returning a permission map instead would be one forge's shape wearing
// an interface, and every consumer would then have to learn that forge's
// vocabulary to read it.
//
// THE ZERO VALUE IS THE SAFE ANSWER. A caller that drops the error reads
// AccessNone, so every way of getting this wrong denies.
type Access uint8

const (
	// AccessNone is "the forge was asked, and said no". It is also the zero
	// value, deliberately.
	AccessNone Access = iota

	// AccessRead is what a public repository grants everyone. IT AUTHORIZES
	// NOTHING. Reading visibility as permission is the defect this rung
	// exists to prevent: every repository on a forge is readable by someone,
	// so a check that accepts "I can see it" accepts strangers.
	AccessRead

	// AccessWrite is "may push". It is what a repository registration and a
	// project link are checked against, and it is v1's admin|maintain|push
	// predicate preserved exactly.
	AccessWrite

	// AccessAdmin is "may administer this repository's settings" — its
	// webhooks, its deploy keys, its existence. It is a real rung and not a
	// speculative one: creating a webhook needs more than push on GitHub and
	// more than Developer on GitLab, and deleting a repository is a question
	// v1's CLI answers by string-matching an error body.
	AccessAdmin
)

func (a Access) CanWrite() bool { return a >= AccessWrite }
func (a Access) CanAdmin() bool { return a >= AccessAdmin }

func (a Access) String() string {
	switch a {
	case AccessRead:
		return "read"
	case AccessWrite:
		return "write"
	case AccessAdmin:
		return "admin"
	default:
		return "none"
	}
}

// Identity is who a credential belongs to. THERE IS NO NUMERIC FIELD.
//
// Subject is the forge's own stable handle for this identity, as an OPAQUE
// host-scoped string — GitHub's user number, Bitbucket's account UUID. Nothing
// in this package parses, splits, orders or compares it, and it may be empty.
// It is a USER id and not a repository id, so repokey's prohibition does not
// reach it; whether tau PERSISTS it as a principal's subject is a product
// decision this layer does not take by having the field.
type Identity struct {
	Login   string
	Subject string
	Email   string // empty is not an error: every forge has different privacy rules
}

// Repository is what only the forge knows. Everything derivable from the key is
// absent on purpose, and THERE IS NO ID.
//
// Key MUST be derived by the driver from the clone URL the forge returned, not
// echoed back from the argument. That turns "the forge and tau agree which
// repository this is" from an assumption into a checked fact, and the
// conformance suite checks it: a driver that echoes the argument would hide a
// redirect, a rename or a case-folding disagreement.
type Repository struct {
	Key           repokey.Key
	HTTPS         string
	SSH           string
	DefaultBranch string
	Private       bool // metadata. Never authorization.
}

// Handle is a resource id the FORGE assigned — a webhook's, a deploy key's. It
// is a string because Bitbucket's is a UUID while GitHub's, GitLab's and
// Gitea's are integers, and it is opaque because nothing in tau parses or
// orders one. It never names a repository: repositories are named by
// repokey.Key and by nothing else.
type Handle string

// Protocol is which clone address is wanted. It is a question with two answers
// on every forge and one answer on some self-hosted ones.
type Protocol uint8

const (
	HTTPS Protocol = iota
	SSH
)

func (p Protocol) String() string {
	if p == SSH {
		return "ssh"
	}
	return "https"
}

// Address is the FACTS a clone of one key needs on one host, and never a
// credentialed URL. pkg/git splices a token into the remote URL — which then
// lands in .git/config on disk, in process arguments, and in every error
// quoting the remote — and it does that by string-replacing the literal
// "github.com", so it is wrong on every other host twice over. The token
// belongs in the transport's credential; what varies per host is the HTTP
// basic-auth username (Bitbucket requires the literal "x-token-auth"; pkg/git
// hardcodes "taubyte") and the SSH account.
type Address struct {
	URL          string
	HTTPUsername string // set for HTTPS
	SSHAccount   string // set for SSH
}
