package schema

import "github.com/taubyte/tau/pkg/tcc/engine"

// WHICH FIELDS MAY BE WRITTEN PER ENVIRONMENT — as a property, not a list.
//
// A list would be a parallel table: right the day it is written and wrong the day the
// twenty-sixth kind arrives, with nothing to say which. So the rule is stated once and
// applied over the population, the way attachAccess and attachIndexes are.
//
// AN ATTRIBUTE MAY VARY UNLESS IT STATES SOMETHING TWO ENVIRONMENTS OF ONE RESOURCE
// MUST SHARE. Four things qualify, and three of them the schema already declares about
// itself:
//
//   - IDENTITY — `id`, `name`, anything Generated or Derived, and anything whose value
//     is claimed OUTSIDE the project: a zone's apex (EmitValidation proves ownership of
//     one example.com) and the block a kind AnswersAt. Environments are yours; the DNS
//     root is not.
//   - WHAT THE BUILD CONSUMES — `source`, `execution/call`, `implements`,
//     `instructions`. A branch resolves to exactly one environment, so "this
//     environment runs different code" already has a spelling, and a suffix would be a
//     second one. Note `image` is NOT here: an image is pulled rather than built, a
//     coordinate like an external database's host, and a rollout changes a digest
//     without making a new application.
//   - WHAT IT POINTS AT — every Ref and the `namespace` that says where to look. What a
//     resource is SET to may vary per environment; what it REACHES may not, or the
//     graph cannot be read without picking one. `model/name` is the single, deliberate
//     exception, marked by hand where it is declared.
//   - SHAPE — a captured switch, or any path whose last segment is not a literal. This
//     clause is already mechanical: envPath returns nil for an Either branch, so
//     `engine/kind` cannot be enved even if something marks it.
//
// The rule admits a few fields nobody will ever vary — `serve/mode`, `timezone`,
// `object/versioning`. That is the trade: a property that also had to exclude every
// field nobody has thought of yet is the curated list again, wearing a rule's clothes.
func attachPerEnv() {
	for _, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		walkPerEnv(g.Children[0])
	}
}

func walkPerEnv(n *engine.Node) {
	// THE ACCESS BLOCK IS THE GRAPH, whole. An entry's `ops` is a capability and its
	// `namespace` says which resource that capability is over, so a suffix here is
	// preview writing where production reads. It is clause three, and it needs to be
	// said about the block rather than derived from each field because an access
	// target is a map KEY rather than a Ref — the one edge nothing resolves at compile
	// time, recorded as a known cost of naming the target once with its ops beside it.
	if AnAccessGroup(n) {
		return
	}
	for _, a := range n.Attributes {
		if a.Meta["perEnv"] == true {
			continue // marked by hand where the rule would refuse it
		}
		if perEnvExcludes(n, a) != "" {
			continue
		}
		engine.PerEnv()(a)
	}
	for _, c := range n.Children {
		walkPerEnv(c)
	}
}

// perEnvExcludes names the clause that refuses this attribute, or "" if it may vary.
// It returns the REASON rather than a bool so a test can print the whole population
// with the rule's own words beside each refusal.
func perEnvExcludes(n *engine.Node, a *engine.Attribute) string {
	path := a.Path
	if len(path) == 0 {
		path = []engine.StringMatch{a.Name}
	}

	// SHAPE, first, because it is the one the loader enforces anyway.
	if a.Captures {
		return "shape: it captures which branch a switch took"
	}
	if _, literal := path[len(path)-1].(string); !literal {
		// The same test envPath makes: a suffix goes on the last segment, and an
		// Either branch is a key the DOCUMENT chooses rather than one the schema
		// names. Checked here rather than by calling into the engine, so the rule
		// reads in one place.
		return "shape: its last segment is a branch key, not a name the schema gives"
	}

	// IDENTITY.
	if len(path) == 1 && (a.Name == "id" || a.Name == "name") {
		return "identity: two environments of one resource are one resource"
	}
	if a.Meta["generated"] != nil || a.Meta["derived"] != nil {
		return "identity: nobody writes it, so nobody writes it per environment"
	}
	if a.Meta["emitValidation"] != nil {
		return "identity: its value is claimed outside the project and proved there"
	}
	if at := engine.AnswersAtOf(n); at != "" {
		if first, ok := path[0].(string); ok && first == at {
			return "identity: the address this kind answers at"
		}
	}

	// WHAT THE BUILD CONSUMES.
	if a.Meta["content"] == true {
		return "content: the branch already says which environment runs which code"
	}

	// A CLOSED SET IS A KIND, NOT A QUANTITY. `runtime: wasm|container`,
	// `protocol`, `tls/mode`, `format`, `metric`, `serve/mode` — an enum names which
	// KIND of thing this is, and an environment that runs a different kind is not the
	// same deployment tested twice. It is the same argument as the Either clause above,
	// for a switch the vocabulary spells as a value rather than as a key.
	if a.Meta["enum"] != nil {
		return "kind: a closed set says which kind of thing this is, and every environment runs the same kind"
	}

	// WHAT IT POINTS AT.
	if a.Meta["ref"] != nil {
		return "reference: what a resource reaches is the graph, and the graph is one graph"
	}
	if last, ok := path[len(path)-1].(string); ok && last == "namespace" && siblingRefs(n, path) {
		return "reference: it says where to look for the thing beside it"
	}
	return ""
}

// siblingRefs reports whether some attribute under the same prefix carries a Ref — the
// test for a `namespace` that qualifies one rather than naming a scope of its own.
func siblingRefs(n *engine.Node, path []engine.StringMatch) bool {
	prefix := engine.FieldName(engine.AKey(literalSegments(path[:len(path)-1])))
	for _, o := range n.Attributes {
		if o.Meta["ref"] == nil || len(o.Path) < 2 {
			continue
		}
		if engine.FieldName(engine.AKey(literalSegments(o.Path[:len(o.Path)-1]))) == prefix {
			return true
		}
	}
	return false
}

func literalSegments(path []engine.StringMatch) []string {
	out := make([]string, 0, len(path))
	for _, seg := range path {
		s, _ := seg.(string)
		out = append(out, s)
	}
	return out
}

// Content marks a field the BUILD reads: which code an artifact is made from. It is
// separate from a reference because it is not a graph edge — nothing reaches a source
// — and separate from a value because a branch already selects it per environment.
func Content() engine.Option { return engine.Annotate("content", true) }
