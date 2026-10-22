package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

func New(schema Schema, options ...yaseer.Option) (Engine, error) {
	return NewWith(schema, nil, options...)
}

// NewWith is New for a project that gives its variables VALUES. `values` is the closed
// set each may take here, which the loader needs whichever binding is being read: one
// document carries every value's spelling at once, so without the whole set a read for
// one value rejects another's key as a typo.
//
// WHICH value each variable holds is not here — it is an argument to ProcessFor, because
// one parse serves several readings and they share this engine and its parsed documents.
// Binding it to the instance would mean re-reading the tree once per reading, which is
// the cost that shows up first when this is compiled to wasm and asked to answer in
// under 20ms.
//
// The order is the ROOT'S, not the caller's: a map has none, and precedence between two
// variables is a statement the vocabulary makes with Varying.
func NewWith(schema Schema, values map[string][]string, options ...yaseer.Option) (Engine, error) {
	sr, err := yaseer.New(options...)
	if err != nil {
		return nil, fmt.Errorf("parser failed to created seer with %w", err)
	}

	def := schema.(*schemaDef)
	vars, err := variablesFor(def.root, values)
	if err != nil {
		return nil, err
	}

	return &instance{
		schema: def,
		seer:   sr,
		vars:   vars,
	}, nil
}

// variablesFor pairs what the vocabulary DECLARES with what this project gives it, and
// refuses the three ways that pairing can be wrong. All three are structural — they are
// about the schema and the options, not about any document — so they are answered once
// here rather than surfacing later as an error against somebody's config.
func variablesFor(root *Node, values map[string][]string) (Vars, error) {
	declared := VariablesOf(root)
	known := make(map[string]bool, len(declared))
	for _, name := range declared {
		known[name] = true
	}

	// A VALUE SET FOR A VARIABLE THE VOCABULARY DOES NOT HAVE is a caller naming an axis
	// that does not exist. Silently ignored, it reads as an axis that exists and never
	// applies, which is the harder bug of the two.
	for _, name := range sorted(values) {
		if !known[name] {
			return nil, fmt.Errorf("engine: values given for %q, which this schema does not declare as a variable (it declares %s)", name, listOrNone(declared))
		}
	}

	vars := make(Vars, 0, len(declared))
	for _, name := range declared {
		vars = append(vars, Var{Name: name, Values: append([]string(nil), values[name]...)})
	}

	// AN ATTRIBUTE CANNOT VARY BY WHAT THE ROOT DID NOT DECLARE. The mark and the
	// declaration are written in two places, so this is the join between them.
	if err := checkVaries(root, known, declared); err != nil {
		return nil, err
	}

	// TWO VARIABLES OF ONE ATTRIBUTE MUST NOT SHARE A VALUE, because `<key>_<value>`
	// would then be two keys spelled the same and the document could not say which it
	// meant. Only where one attribute varies by both: two unrelated axes are free to
	// admit the same word.
	return vars, checkCollision(root, vars)
}

func checkVaries(root *Node, known map[string]bool, declared []string) error {
	var walk func(*Node) error
	walk = func(n *Node) error {
		if n == nil {
			return nil
		}
		for _, attr := range n.Attributes {
			for _, name := range VariesBy(attr) {
				if !known[name] {
					return fmt.Errorf("engine: attribute %q varies by %q, which the root does not declare (it declares %s)", attr.Name, name, listOrNone(declared))
				}
			}
		}
		for _, child := range n.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

func checkCollision(root *Node, vars Vars) error {
	var walk func(*Node) error
	walk = func(n *Node) error {
		if n == nil {
			return nil
		}
		for _, attr := range n.Attributes {
			by := VariesBy(attr)
			if len(by) < 2 {
				continue
			}
			from := map[string]string{}
			for _, name := range by {
				for _, value := range vars.Values(name) {
					if other, taken := from[value]; taken {
						return fmt.Errorf("engine: attribute %q varies by both %q and %q, and both admit the value %q — %s_%s would mean two things", attr.Name, other, name, value, attr.Name, value)
					}
					from[value] = name
				}
			}
		}
		for _, child := range n.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

func sorted(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func (s *instance) Process() (object.Object[object.Refrence], error) {
	return s.ProcessFor(nil)
}

// ProcessFor reads the project UNDER ONE BINDING: every attribute that varies by a bound
// variable takes its `<key>_<value>` spelling where the document writes one. The
// documents are parsed once and read many times, which is the point of it being an
// argument.
func (s *instance) ProcessFor(b Bound) (object.Object[object.Refrence], error) {
	bound, err := s.bind(b)
	if err != nil {
		return nil, err
	}
	return load(s.schema.root, s.seer.Query(), s.walk(bound))
}

// bind drops the variables this read says nothing about and refuses the one binding that
// cannot be honoured.
//
// A BINDING FOR A VARIABLE WITH NO VALUES IS REFUSED HERE, not swallowed. Nothing claims
// a suffix for it, so strict mode would reject the author's own override as an unknown
// key — an error against the config for a mistake made in the options. It is checked at
// the read rather than in NewWith because the binding is the read's argument.
//
// An EMPTY value is not a binding: it is how a caller says "the bare key", which is what
// a project that has no values for the variable reads anyway.
func (s *instance) bind(b Bound) (Bound, error) {
	if len(b) == 0 {
		return nil, nil
	}
	out := make(Bound, len(b))
	for _, name := range sortedBound(b) {
		value := b[name]
		if value == "" {
			continue
		}
		if !s.vars.Has(name) {
			return nil, fmt.Errorf("engine: read bound %q to %q, but this project declares no values for it — nothing may be written per %s", name, value, name)
		}
		out[name] = value
	}
	return out, nil
}

func sortedBound(b Bound) []string {
	out := make([]string, 0, len(b))
	for k := range b {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *instance) walk(b Bound) walk {
	return walk{bound: b, vars: s.vars}
}
