// Package storekey is the one rule about a key a guest supplied: it cannot leave the
// prefix it was given.
//
// Resident stores address an entry as `<prefix><key>`, the prefix being the tenant
// and the key the workload's. The stores put the join through ds.NewKey
// (path.Clean), so `..` collapses before anything is read or written:
//
//	"shopA/prod/cache-id/" + "../../../victim/prod/their-cache/session"
//	    -> /victim/prod/their-cache/session
//
// It validates and does not rewrite, and it checks the cleaned result against the
// cleaned prefix rather than a list of forbidden spellings.
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

// Canonical answers whether a whole key is already what the store will make of it:
// no "..", no "." and no doubled separators. It is for a key built from a fixed
// prefix plus outside text (pkg/register's "h/"+fqdn), where ds.NewKey would collapse
// ".." into another project's row:
//
//	"h/" + "../p/victim/prod"  ->  /p/victim/prod
func Canonical(key string) error {
	if key == "" {
		return nil
	}
	if path.Clean("/"+key) != "/"+key {
		return fmt.Errorf("%w: %q", ErrNotCanonical, key)
	}
	return nil
}
