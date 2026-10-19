package secrets

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
)

// Ref names one secret: a project, one of its environments, and a name.
//
// ALL THREE ARE PATH SEGMENTS, and each is checked on its own. A secret's
// address is the key it is stored under and the data its encryption is bound
// to, so a part that could hold a "/" would let two different references
// spell one key.
type Ref struct {
	Project     string
	Environment string
	Name        string
}

func (r Ref) String() string { return r.Project + "/" + r.Environment + "/" + r.Name }

// Validate refuses a reference that is not three clean segments.
func (r Ref) Validate() error {
	if err := validScope(r.Project, r.Environment); err != nil {
		return err
	}
	return validPart("name", r.Name)
}

func validScope(project, environment string) error {
	if err := validPart("project", project); err != nil {
		return err
	}
	return validPart("environment", environment)
}

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

// contentAAD binds a ciphertext to the secret it belongs to, so a stored
// ciphertext moved under another name does not open.
func contentAAD(ref Ref) []byte {
	return []byte("tau/secrets/content/v2|" + ref.String())
}

// wrapAAD binds a wrapping to its secret and to the key it was made under.
func wrapAAD(ref Ref, keyID string) []byte {
	return []byte("tau/secrets/wrap/v2|" + keyID + "|" + ref.String())
}

var (
	// ErrNotSet is a secret no member holds.
	ErrNotSet = errors.New("secrets: nothing is set under that name")

	// ErrUnavailable is a secret that exists and could not be opened, because
	// not enough members answered.
	ErrUnavailable = errors.New("secrets: unavailable")

	// ErrInvalidRef is a reference that is not three clean segments.
	ErrInvalidRef = errors.New("secrets: invalid reference")
)
