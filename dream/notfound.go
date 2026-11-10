package dream

import (
	"fmt"
	"slices"
	"strings"
)

// UniverseNotFoundError names a universe Dream does not have, and the ones it has.
type UniverseNotFoundError struct {
	Name     string
	Existing []string
}

func (e *UniverseNotFoundError) Error() string {
	if len(e.Existing) == 0 {
		return fmt.Sprintf("universe `%s` not found: Dream has none; start one with `dream new universe %s`", e.Name, e.Name)
	}
	return fmt.Sprintf("universe `%s` not found; Dream has: %s", e.Name, strings.Join(e.Existing, ", "))
}

// notFound is the error for a missing universe; the caller holds universesLock.
func (m *Multiverse) notFound(name string) error {
	existing := make([]string, 0, len(m.universes))
	for n := range m.universes {
		existing = append(existing, n)
	}
	slices.Sort(existing)
	return &UniverseNotFoundError{Name: name, Existing: existing}
}
