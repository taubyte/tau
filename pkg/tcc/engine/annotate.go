package engine

// Annotate attaches opaque metadata to an attribute. The engine stores it and
// never interprets it — only code generators (and other out-of-band tooling)
// read it. This is the generic hook that domain-specific vocabulary is built on.
func Annotate(key string, val any) Option {
	return func(a *Attribute) {
		if a.Meta == nil {
			a.Meta = map[string]any{}
		}
		a.Meta[key] = val
	}
}

// Duration and Bytes are String attributes tagged with a scalar-codec name. The
// engine treats them exactly as strings — parsing of "20s"/"32GB" into typed
// values stays in the transform passes. The tag only tells a code generator the
// concrete Go type to emit (e.g. uint64). It has no runtime effect.
func Duration(name string, opts ...Option) *Attribute {
	return String(name, append(opts, Annotate("scalar", "duration"))...)
}

func Bytes(name string, opts ...Option) *Attribute {
	return String(name, append(opts, Annotate("scalar", "bytes"))...)
}

// Field overrides the Go struct field name a generator emits for this attribute,
// for cases where the config-key-derived name differs from the struct field
// (e.g. github-id -> RepoID). Generation-only; no runtime effect.
func Field(goName string) Option {
	return Annotate("field", goName)
}

// Tag sets the mapstructure key a generator emits for this attribute's struct
// field, when the on-disk key isn't derivable from the schema (e.g. github-id ->
// `mapstructure:"repository-id"`). Generation-only; no runtime effect.
func Tag(key string) Option {
	return Annotate("tag", key)
}

// NodeOption mutates a node — the node-level analogue of Option. Passed to
// DefineIter to attach opaque metadata to the object template (the node each
// compiled resource-object matches), not to the parse rules.
type NodeOption func(*Node)

// GroupAnnotate attaches opaque metadata to a node. Like Annotate, the engine
// stores it and never interprets it — only code generators read it.
func GroupAnnotate(key string, val any) NodeOption {
	return func(n *Node) {
		if n.Meta == nil {
			n.Meta = map[string]any{}
		}
		n.Meta[key] = val
	}
}

// Capability is an opaque object-addressing tag. The engine treats it as a plain
// string; the meaning of each value (e.g. "wasm" -> WasmModulePath) lives entirely
// in the code generator.
type Capability = string

// Addressing records the set of TNS-key capabilities a compiled object has, for a
// generator to emit its path helpers. Generation-only; no runtime effect.
func Addressing(caps ...Capability) NodeOption {
	return GroupAnnotate("addressing", caps)
}
