package gen

import (
	"fmt"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// containerKey returns the config key of the nested container group — the one
// whose iterator holds resource sub-groups (applications) rather than being a
// resource itself — or "" if the schema has none. Same structural test the
// struct generator uses to emit the bare container struct.
func containerKey(root []*engine.Node) string {
	for _, g := range root {
		if len(g.Children) == 0 {
			continue
		}
		if _, ok := descriptorFor(g.Children[0]); ok {
			continue
		}
		if it := g.Children[0]; it.Group && len(it.Children) > 0 {
			name, _ := g.Match.(string)
			return name
		}
	}
	return ""
}

// containerSpec is the declared Go name (Singular) of the container group
// (applications -> Application) — the struct type and the parent-container
// accessor both take it. Returns "" if the schema has no container, or an error
// if a container exists with no Singular() (the generator never guesses).
func containerSpec(root []*engine.Node) (string, error) {
	for _, g := range root {
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		if _, ok := descriptorFor(iter); ok {
			continue
		}
		if iter.Group && len(iter.Children) > 0 {
			if s, ok := iter.Meta["singular"].(string); ok && s != "" {
				return s, nil
			}
			name, _ := g.Match.(string)
			return "", fmt.Errorf("container group %q has no Singular() declaration", name)
		}
	}
	return "", nil
}
