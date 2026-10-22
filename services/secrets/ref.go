package secrets

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"

	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
)

// Scope is where a secret lives: one of the cloud's own services, or one
// project environment. Exactly one kind is set.
//
// The kind is the first segment of every stored path, so a project named
// "cloud" is still a project: it can never spell a cloud secret's key.
type Scope struct {
	Service     string
	Project     string
	Environment string
}

// Cloud is the scope a cloud service keeps its own secrets in.
func Cloud(service string) Scope { return Scope{Service: service} }

// Project is the scope of one project environment.
func Project(project, environment string) Scope {
	return Scope{Project: project, Environment: environment}
}

const (
	cloudKind   = "cloud"
	projectKind = "project"
)

func (s Scope) segments() []string {
	if s.Service != "" {
		return []string{cloudKind, s.Service}
	}
	return []string{projectKind, s.Project, s.Environment}
}

func (s Scope) String() string { return strings.Join(s.segments(), "/") }

// Validate refuses a scope that is neither kind, or both, or whose parts are
// not clean path segments.
func (s Scope) Validate() error {
	switch {
	case s.Service != "" && (s.Project != "" || s.Environment != ""):
		return fmt.Errorf("%w: a scope is a cloud service or a project, not both", ErrInvalidRef)
	case s.Service != "":
		return validPart("service", s.Service)
	}
	if err := validPart("project", s.Project); err != nil {
		return err
	}
	return validPart("environment", s.Environment)
}

// Ref names one secret: a scope and a name.
//
// EVERY PART IS A PATH SEGMENT, and each is checked on its own. A secret's
// address is the key it is stored under and the data its encryption is bound
// to, so a part that could hold a "/" would let two different references
// spell one key.
type Ref struct {
	Scope
	Name string
}

func (r Ref) String() string { return r.Scope.String() + "/" + r.Name }

// Validate refuses a reference that is not a valid scope and a clean name.
func (r Ref) Validate() error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	return validPart("name", r.Name)
}

// version is one written value of a secret, named by the CID of its
// ciphertext. A write never replaces a version; it adds one.
type version struct {
	Ref
	CID string
}

func (v version) String() string { return v.Ref.String() + "@" + v.CID }

const maxPart = 128

func validPart(what, part string) error {
	switch {
	case part == "":
		return fmt.Errorf("%w: %s cannot be empty", ErrInvalidRef, what)
	case len(part) > maxPart:
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidRef, what, maxPart)
	case strings.ContainsRune(part, '/'):
		return fmt.Errorf("%w: %s cannot contain '/'", ErrInvalidRef, what)
	case path.Clean("/"+part) != "/"+part:
		return fmt.Errorf("%w: %s is not a path segment: %q", ErrInvalidRef, what, part)
	}
	for _, c := range part {
		if !unicode.IsPrint(c) || unicode.IsSpace(c) {
			return fmt.Errorf("%w: %s contains an unprintable character %q", ErrInvalidRef, what, c)
		}
	}
	return nil
}

// contentID is the CID of a ciphertext, which names its version.
func contentID(ciphertext []byte) string {
	sum, err := multihash.Sum(ciphertext, multihash.SHA2_256, -1)
	if err != nil {
		// SHA2_256 at its default length cannot fail.
		panic(err)
	}
	return cid.NewCidV1(cid.Raw, sum).String()
}

// contentAAD binds a ciphertext to the secret it belongs to, so a stored
// ciphertext moved under another name does not open.
func contentAAD(ref Ref) []byte {
	return []byte("tau/secrets/content/v3|" + ref.String())
}

// wrapAAD binds a wrapping to its secret, to the version it unlocks and to the
// key it was made under.
func wrapAAD(v version, keyID string) []byte {
	return []byte("tau/secrets/wrap/v3|" + keyID + "|" + v.CID + "|" + v.Ref.String())
}

var (
	// ErrNotSet is a secret no member holds.
	ErrNotSet = errors.New("secrets: nothing is set under that name")

	// ErrUnavailable is a secret that exists and could not be opened, because
	// not enough members answered.
	ErrUnavailable = errors.New("secrets: unavailable")

	// ErrInvalidRef is a reference that is not a valid scope and name.
	ErrInvalidRef = errors.New("secrets: invalid reference")
)
