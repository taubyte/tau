package engine

// Document layout introspection: where a DSL's resources keep their fields on
// disk. The engine already encodes this in dump.go (a container's instances are
// directories, each with a NodeDefaultSeerLeaf document; every other resource is
// one file). These read it back out so a caller — a session, a CLI, a UI — maps
// a resource address to its document without re-deriving the convention.

// ContainerDoc returns the document name a container group's instances keep
// their own fields in ("config"), or "" when group is not a container. A
// container is a group whose iterator is ITSELF a group over all names
// (DefineIterGroup) — the same structural test dump.go uses to decide that its
// instances are directories holding nested resources.
func ContainerDoc(root []*Node, group string) string {
	for _, g := range root {
		name, _ := g.Match.(string)
		if name != group || len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		if _, all := iter.Match.(StringMatchAll); all && iter.Group {
			return NodeDefaultSeerLeaf
		}
	}
	return ""
}

// Groups lists every top-level group key the DSL declares (resources, the
// container, leaf maps) — the directory names a config repo may hold.
func Groups(root []*Node) []string {
	out := make([]string, 0, len(root))
	for _, g := range root {
		if name, ok := g.Match.(string); ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}
