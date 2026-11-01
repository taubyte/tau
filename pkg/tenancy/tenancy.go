// Package tenancy says which forge namespaces a cloud serves.
//
// A namespace is a forge host and the owning segment under it (a GitHub org, a GitLab
// group), written "github.com/acme". A cloud that declares any serves repositories
// under those and refuses every other by name; one that declares none serves whatever
// its forges grant, the default.
//
// The namespace is the unit, not the caller: the forge already granted the caller
// write access, and checking membership would need a credential the cloud does not
// hold.
package tenancy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// Tenancy is a parsed, canonical set of namespaces. The zero value serves
// everything.
type Tenancy struct {
	namespaces []string
}

// Parse canonicalises declared namespaces: lowercased, the host's default port
// dropped, duplicates removed, sorted. An entry that is not exactly
// "host/namespace" is refused, so a subgroup or a repository written where a
// namespace was meant is a boot error rather than a cloud that serves nothing.
func Parse(entries []string) (Tenancy, error) {
	var out []string
	for _, entry := range entries {
		ns, err := canonical(entry)
		if err != nil {
			return Tenancy{}, err
		}
		if !slices.Contains(out, ns) {
			out = append(out, ns)
		}
	}
	slices.Sort(out)
	return Tenancy{namespaces: out}, nil
}

func canonical(entry string) (string, error) {
	trimmed := strings.Trim(strings.TrimSpace(entry), "/")
	// repokey parses a repository, so a namespace is parsed as one with a
	// placeholder name and must come back with exactly one owning segment.
	key, err := repokey.Parse("https://" + trimmed + "/x")
	if err != nil || key.Owner == "" || strings.Contains(key.Owner, "/") {
		return "", fmt.Errorf("tenancy: %q is not a namespace; write it as host/namespace, e.g. github.com/acme", entry)
	}
	return key.Host + "/" + key.Owner, nil
}

// Namespaces are the canonical entries, or nil when none were declared.
func (t Tenancy) Namespaces() []string { return slices.Clone(t.namespaces) }

// Configured reports whether any namespace was declared.
func (t Tenancy) Configured() bool { return len(t.namespaces) > 0 }

// Serves reports whether key belongs to a declared namespace, or true when
// none was declared. The comparison is on repokey's canonical, lowercased
// form, so a spelling differing only in case is the same namespace.
func (t Tenancy) Serves(key repokey.Key) bool {
	if !t.Configured() {
		return true
	}
	return slices.Contains(t.namespaces, key.Host+"/"+key.Namespace())
}

// Refuse is nil when key is served, and otherwise says which repository is
// outside which tenancy, in words an operator or a developer can act on.
func (t Tenancy) Refuse(key repokey.Key) error {
	if t.Serves(key) {
		return nil
	}
	return fmt.Errorf("%s is outside this cloud's tenancy; it serves %s", key, strings.Join(t.namespaces, ", "))
}
