// Package storekey is the one rule about a key a GUEST supplied: that it cannot
// leave the prefix it was given.
//
// WHY IT EXISTS. Every resident store addresses an entry as `<prefix><key>`,
// where the prefix is the tenant — `project/environment/<model>/<id>/` — and
// the key is whatever a workload passed. Spec.Prefix already refuses a `/` in
// any segment IT builds, which is the same concern from the other end; nothing
// checked the half the guest wrote.
//
// WHAT THAT COST. The stores below all put the joined string through
// ds.NewKey, which is path.Clean, so `..` segments collapse BEFORE anything is
// read or written:
//
//	"shopA/prod/cache-id/" + "../../../victim/prod/their-cache/session"
//	    -> /victim/prod/their-cache/session
//
// which is a cross-tenant read and a cross-tenant write, from a key a guest
// controls entirely.
//
// IT VALIDATES AND DOES NOT REWRITE. Returning a cleaned key would change what
// legitimate keys address, so the caller still joins the two itself and the
// stored key is whatever ds.NewKey makes of it — this only answers whether that
// result is still inside the prefix.
//
// THE CHECK IS ON THE RESULT, not on a list of forbidden spellings. A blacklist
// of ".." is a guess about what path.Clean does; cleaning the join and asking
// whether it still starts with the cleaned prefix is the same question the
// store will answer later, asked early.
package storekey

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// ErrEscapes is a key that addresses something outside its own prefix.
var ErrEscapes = errors.New("storekey: that key leaves the store it was given")

// Check answers whether prefix+key still addresses something inside prefix.
//
// An empty key is the caller's to refuse — each store has its own sentinel for
// it, and they say different things.
func Check(prefix, key string) error {
	if key == "" {
		return nil
	}

	root := path.Clean("/" + prefix)
	full := path.Clean("/" + prefix + key)
	if full == root || strings.HasPrefix(full, strings.TrimSuffix(root, "/")+"/") {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrEscapes, key)
}

// ErrNotCanonical is a key that path.Clean would change, which means the key
// that gets stored is not the key that was asked for.
var ErrNotCanonical = errors.New("storekey: that key is not the key it would be stored under")

// Canonical answers whether a WHOLE key is already what the store will make of
// it — no "..", no "." and no doubled separators.
//
// IT IS THE RULE FOR A KEY BUILT FROM A FIXED PREFIX rather than joined onto a
// tenant's. pkg/register addresses rows as "h/"+fqdn, "p/"+project+"/"+env and
// so on, where the prefix is one character this package chose and the rest is
// somebody else's text: a fqdn out of a Host header, a project out of a
// publication. There is no tenant prefix to stay inside, so the question is not
// "does this escape" but "is this the key it looks like" -- and a key holding
// ".." is not, because ds.NewKey collapses it first:
//
//	"h/" + "../p/victim/prod"  ->  /p/victim/prod
//
// which reads, and on a publish WRITES, another project's row.
//
// A legitimate fqdn, project or environment holds none of these, so refusing
// them costs nothing that was ever addressable.
func Canonical(key string) error {
	if key == "" {
		return nil
	}
	if path.Clean("/"+key) != "/"+key {
		return fmt.Errorf("%w: %q", ErrNotCanonical, key)
	}
	return nil
}
