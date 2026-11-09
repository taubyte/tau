package register

import (
	"context"
	"slices"
	"strings"
)

// Depends is what one read's answer is a function of: every key it read and every
// prefix it listed. A change to anything else cannot change the answer, so a cache
// that drops the answer when one of these changes is never stale for longer than
// the change takes to arrive.
type Depends struct {
	Keys     []string
	Prefixes []string
}

// Covers says whether a change to key could change an answer that depends on d.
func (d Depends) Covers(key string) bool {
	key = strings.TrimPrefix(key, sep)
	if slices.Contains(d.Keys, key) {
		return true
	}
	return slices.ContainsFunc(d.Prefixes, func(p string) bool { return strings.HasPrefix(key, p) })
}

// Within says whether everything d depends on is already in held, which is how a
// second read shows that nothing new has to be watched.
func (d Depends) Within(held Depends) bool {
	for _, k := range d.Keys {
		if !held.Covers(k) {
			return false
		}
	}
	for _, p := range d.Prefixes {
		if !slices.Contains(held.Prefixes, p) {
			return false
		}
	}
	return true
}

// Recorded is r reading through a store that notes what each read touches, and that
// note. It is for one read at a time: a read is a sequence of store calls, and the
// note is what they were.
func (r *Register) Recorded() (*Register, *Depends) {
	d := &Depends{}
	return &Register{store: recording{Store: r.store, d: d}}, d
}

type recording struct {
	Store
	d *Depends
}

func (s recording) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if key = strings.TrimPrefix(key, sep); !slices.Contains(s.d.Keys, key) {
		s.d.Keys = append(s.d.Keys, key)
	}
	return s.Store.Get(ctx, key)
}

func (s recording) List(ctx context.Context, prefix string) ([]string, error) {
	if p := strings.TrimPrefix(prefix, sep); !slices.Contains(s.d.Prefixes, p) {
		s.d.Prefixes = append(s.d.Prefixes, p)
	}
	return s.Store.List(ctx, prefix)
}
