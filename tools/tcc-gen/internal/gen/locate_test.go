package gen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Pins where a value sits in a compiled object. One storage block becomes a nested
// value, a hoisted value and a discriminator:
//
//	object:                 ->  {"object": {"versioning": true},
//	    versioning: true          "size": 1000000000,
//	    size: 1GB                 "type": "object"}

func locationOf(t *testing.T, kind, path string) Located {
	t.Helper()
	for _, l := range Locations(v2.GenerationRoot(), kind) {
		if l.Path() == path {
			return l
		}
	}
	t.Fatalf("%s declares no %q", kind, path)
	return Located{}
}

func TestAFieldUnderABranchCompilesSomewhereElse(t *testing.T) {
	size := locationOf(t, "storage", "size")

	require.Equal(t, []string{"size"}, size.Key,
		"a reader addresses the compiled key; object/size is where it was authored")
	require.Equal(t, "{object|streaming}/size", strings.Join(size.Authored, "/"))
	require.Equal(t, []string{"object", "streaming"}, size.Branch,
		"without the branch a reader takes one store's size for another's")
	require.Equal(t, "Int", size.Read, "1GB compiled to a number")
}

func TestACapturedBranchIsReportedAsTheDiscriminator(t *testing.T) {
	model := locationOf(t, "storage", "type")
	require.True(t, model.Captures)
	require.Equal(t, []string{"object", "streaming"}, model.Branch)

	handler := locationOf(t, "gateway", "route/handler/kind")
	require.True(t, handler.Captures, "a kind may be discriminated twice")
	require.Equal(t, []string{"function", "application", "package", "gateway", "mcp", "agent", "bus"}, handler.Branch,
		"declaration order, which is the order an author reads them in")

	// A PLAIN SEGMENT IS NOT A BRANCH. `route/cache/ttl` is authored under literals,
	// so there is nothing to choose and nothing to report.
	ttl := locationOf(t, "gateway", "route/cache/ttl")
	require.Empty(t, ttl.Branch)
	require.Equal(t, "route/cache/ttl", strings.Join(ttl.Authored, "/"))
}

func TestTheReaderIsChosenByTheDeclaredType(t *testing.T) {
	require.Equal(t, "Duration", locationOf(t, "storage", "streaming/ttl").Read)
	require.Equal(t, "Int", locationOf(t, "storage", "instances/limit").Read)
	require.Equal(t, "Bool", locationOf(t, "storage", "object/versioning").Read)
	require.Equal(t, "Strings", locationOf(t, "storage", "tags").Read)
	require.Equal(t, "String", locationOf(t, "storage", "name").Read)
}

func TestTheRenderedReaderAddressesCompiledKeys(t *testing.T) {
	out := string(RenderReaders("specs", v2.GenerationRoot(), []string{"storage"}))

	require.Contains(t, out, `out.Size, _ = r.Int("size")`,
		"the hoisted field is read where it compiles")
	require.Contains(t, out, `out.Versioning = present[bool](r.Bool("object", "versioning"))`,
		"a nested one is read where it nests, in its block")
	require.Contains(t, out, `out.Ttl = present[time.Duration](r.Duration("streaming", "ttl"))`)
	require.Contains(t, out, `// authored at {object|streaming}/size`,
		"and the generated source says where the value came from")
	require.Contains(t, out, `// which of object, streaming was written`)
}

func TestAKindsFieldsAreNotWhatItHolds(t *testing.T) {
	root := v2.GenerationRoot()

	// THE SCOPE IS THE CASE THAT PROVED IT: the namespace reader came out with
	// one field per attribute in the whole vocabulary, addressing keys that in a
	// namespace object are directories of resources.
	var ns []string
	for _, l := range Locations(root, "namespace") {
		ns = append(ns, l.Path())
	}
	require.ElementsMatch(t, []string{"description", "id", "name", "regions/topology", "regions/in", "tags"}, ns,
		"a namespace has its identity and its regions; everything else in it is a resource")

	// AND NO KIND READS AN ACCESS ENTRY AS A FIELD OF ITS OWN. The ops live at
	// `<kind>/<label>/ops` — a map of labels — so a flat read of the bare key is
	// one that can never find anything.
	ops := v2.AccessEntryShape().Ops
	for _, kind := range v2.Kinds() {
		for _, l := range Locations(root, kind) {
			require.NotEqual(t, ops, l.Path(), "%s reads an access entry's %q as its own", kind, ops)
		}
	}
}

func TestAUnionCarriesAGroupWhole(t *testing.T) {
	out := string(RenderReaders("specs", v2.GenerationRoot(), []string{"application", "function"}))

	require.Contains(t, out, "type WorkloadBlock struct {")
	require.Contains(t, out, "\tVariables          map[string]VariablesEntry\n")
	require.Contains(t, out, `out.Variables[name] = ReadVariablesEntry(r.Sub("variables", name))`)
	require.Contains(t, out, `out.ExecutionCall = r.String("execution", "call")`,
		"a field only one of the kinds declares is still the union's")
	require.Contains(t, out, `out.Ports[name] = ReadApplicationPortsEntry(r.Sub("ports", name))`)
}

func TestAGroupTwoKindsReadDifferentlyStopsTheGenerator(t *testing.T) {
	kind := func(entry string, list bool) *typeDef {
		return &typeDef{role: "kind", members: []member{{key: []string{"steps"}, sub: &typeDef{role: "entry", name: entry, list: list}}}}
	}
	m := &model{order: []*typeDef{kind("AStep", true), kind("AStep", true)}}
	require.Equal(t, []unionGroup{{key: "steps", entry: "AStep", list: true}}, m.groupsUnder([]string{"steps"}),
		"two kinds agreeing is one field")
	require.Equal(t, "[]AStep", m.groupsUnder([]string{"steps"})[0].goType())
	require.Contains(t, m.groupsUnder([]string{"steps"})[0].read(), `out.Steps = append(out.Steps, ReadAStep(e))`)

	m.order = append(m.order, kind("BStep", true))
	require.Panics(t, func() { m.groupsUnder([]string{"steps"}) })
}

func TestAUnionsDiscriminatorNamesEveryKindsValues(t *testing.T) {
	out := string(RenderReaders("specs", v2.GenerationRoot(), []string{"package", "agent", "application", "function"}))

	require.Contains(t, out, `SourceBlockKeySourceKind`)
	for _, value := range []string{"Image", "Git", "Builtin"} {
		require.Contains(t, out, "SourceBlockSourceKind"+value+" ")
	}
	require.Contains(t, out, `WorkloadBlockKeyVariables`)
	require.Contains(t, out, `WorkloadBlockKeyPackageName`)
	require.Contains(t, out, "which of git, image, builtin was written")
}
