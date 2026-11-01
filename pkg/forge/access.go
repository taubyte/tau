package forge

import "github.com/taubyte/tau/pkg/git/repokey"

// Access is what a principal may do to one repository, in tau's terms: each
// driver maps its forge's roles onto these rungs. The zero value denies.
type Access uint8

const (
	AccessNone Access = iota

	// AccessRead is what a public repository grants everyone, so it authorizes
	// nothing.
	AccessRead

	// AccessWrite is "may push": what a registration and a project link are
	// checked against.
	AccessWrite

	// AccessAdmin may change the repository's webhooks, deploy keys and existence.
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

// Identity is who a credential belongs to. Subject is the forge's stable handle
// for it, opaque and host-scoped, and may be empty.
type Identity struct {
	Login   string
	Subject string
	Email   string // empty is not an error: every forge has different privacy rules
}

// Repository holds only what the forge knows. Key is derived from the clone URL
// the forge returned, never echoed from the argument, so a redirect, a rename
// or a case-folding disagreement shows.
type Repository struct {
	Key           repokey.Key
	HTTPS         string
	SSH           string
	DefaultBranch string
	Private       bool // metadata. Never authorization.
}

// Handle is an id the forge assigned to a webhook or deploy key, opaque. It
// never names a repository.
type Handle string

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

// Address is what a clone needs, never a credentialed URL: the token belongs in
// the transport's credential. What varies per host is the HTTP basic-auth
// username and the SSH account.
type Address struct {
	URL          string
	HTTPUsername string // set for HTTPS
	SSHAccount   string // set for SSH
}
