package schema_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Holds the runtime to pkg/specs' generated readers: a consumer outside pkg/tcc
// and pkg/specs may not spell a path a generated field covers, since a hand
// spelled path drifts from where a captured branch actually compiles.

// spelledAllowed is every spelled read a consumer keeps, by "<file> <path>", and
// why no generated field covers it. Empty is the goal and the current state; an
// entry here is a key the schema genuinely does not describe, never a field
// somebody did not get round to.
var spelledAllowed = map[string]string{}

func TestNoConsumerSpellsAPathAGeneratedFieldCovers(t *testing.T) {
	root := repoRoot(t)
	spelled, _ := scanReads(t, root)
	types := generatedTypes(t, root)

	// EVERY KEY A GENERATED READER DECODES, from each kind's reader and from
	// the cross-kind unions, as patterns: an entry's name is a `*`.
	covered := map[string]bool{}
	for _, group := range schema.V2Resources {
		kind, _ := group.Match.(string)
		if kind == "" {
			continue
		}
		absoluteKeys(types, strings.ToUpper(kind[:1])+kind[1:], nil, 0, covered)
	}
	for _, union := range []string{"SourceBlock", "BuildBlock", "WorkloadBlock"} {
		absoluteKeys(types, union, nil, 0, covered)
	}
	require.NotEmpty(t, covered)

	consulted := map[string]bool{}
	var bad []string
	for file, paths := range spelled {
		if strings.HasPrefix(file, "pkg/tcc/") || strings.HasPrefix(file, "pkg/specs/") {
			continue
		}
		for path := range paths {
			entry := file + " " + path
			if _, ok := spelledAllowed[entry]; ok {
				consulted[entry] = true
				continue
			}
			if key, ok := coveredBy(path, covered); ok {
				bad = append(bad, entry+"  (generated: "+key+")")
			}
		}
	}
	sort.Strings(bad)
	require.Empty(t, bad,
		"these files read a compiled resource by a SPELLED path that a generated field covers. Read it through pkg/specs' generated reader instead (specs.ReadX(r).Field, or a union for a cross-kind read); if the key is genuinely not the schema's, name it in spelledAllowed with why")

	var expired []string
	for entry, reason := range spelledAllowed {
		require.NotEmpty(t, reason, "spelledAllowed carries %s with no reason", entry)
		if !consulted[entry] {
			expired = append(expired, entry)
		}
	}
	sort.Strings(expired)
	require.Empty(t, expired, "spelledAllowed excuses reads nothing makes any more - drop them")
}

// coveredBy reports the generated key a spelled read addresses, if any. Both
// sides may hold a `*` — a computed segment in the read, an entry's name in the
// key — and a read ending in `**` takes a block whole, which covers every key
// under it.
func coveredBy(read string, keys map[string]bool) (string, bool) {
	for key := range keys {
		if overlaps(strings.Split(read, "/"), strings.Split(key, "/")) {
			return key, true
		}
	}
	return "", false
}

func overlaps(read, key []string) bool {
	for i, segment := range read {
		if segment == "**" {
			return i <= len(key)
		}
		if i >= len(key) {
			return false
		}
		if segment == "*" || key[i] == "*" || segment == key[i] {
			continue
		}
		return false
	}
	return len(read) == len(key)
}

func TestTheGuardSeesASpelledRead(t *testing.T) {
	keys := map[string]bool{"route/path": true, "ports/*/number": true, "sql/engine": true}

	for _, read := range []string{"route/path", "route/**", "ports/http/number", "*/engine"} {
		_, ok := coveredBy(read, keys)
		require.True(t, ok, read)
	}
	for _, read := range []string{"route", "route/path/x", "inference/local/scope", "ports/http"} {
		_, ok := coveredBy(read, keys)
		require.False(t, ok, read)
	}
}
