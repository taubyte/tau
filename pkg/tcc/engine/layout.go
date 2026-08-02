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

// GroupInfo identifies one top-level group: the directory key it is authored
// under, the singular name the DSL declares for one instance ("" for a group
// that declares none, e.g. a leaf map), and whether its instances are containers
// holding resources of their own.
type GroupInfo struct {
	Group     string
	Singular  string
	Container bool
}

// Groups lists every top-level group the DSL declares (resources, the container,
// leaf maps) — the directory names a config repo may hold, each with the name a
// consumer addresses one instance of it by.
func Groups(root []*Node) []GroupInfo {
	out := make([]GroupInfo, 0, len(root))
	for _, g := range root {
		name, ok := g.Match.(string)
		if !ok || name == "" {
			continue
		}
		info := GroupInfo{Group: name}
		if len(g.Children) > 0 {
			iter := g.Children[0]
			if r, ok := iter.Meta["resource"].([4]string); ok {
				info.Singular = r[2] // [schemaPkg, iface, specType, specPkg]
			} else if s, ok := iter.Meta["singular"].(string); ok {
				info.Singular = s
			}
			if _, all := iter.Match.(StringMatchAll); all && iter.Group {
				info.Container = true
			}
		}
		out = append(out, info)
	}
	return out
}
