package gen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// locate_test pins the thing the flat models cannot say: where a value SITS in a
// compiled object, as opposed to where it was written.
//
// `storage` is the case that proves it, because one authored block becomes three
// different things — a nested value, a hoisted value and a discriminator:
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

// TestAHOISTEDFIELDISFOUNDWHEREITCOMPILES, not where it was written.
func TestAFieldUnderABranchCompilesSomewhereElse(t *testing.T) {
	size := locationOf(t, "storage", "size")

	require.Equal(t, []string{"size"}, size.Key,
		"a reader addresses the compiled key; object/size is where it was authored")
	require.Equal(t, "{object|streaming}/size", strings.Join(size.Authored, "/"))
	require.Equal(t, []string{"object", "streaming"}, size.Branch,
		"without the branch a reader takes one store's size for another's")
	require.Equal(t, "Int", size.Read, "1GB compiled to a number")
}

// TestTHEDISCRIMINATORSAYSWHICHBRANCHWASTAKEN, and there may be more than one
// on a kind.
func TestACapturedBranchIsReportedAsTheDiscriminator(t *testing.T) {
	model := locationOf(t, "storage", "type")
	require.True(t, model.Captures)
	require.Equal(t, []string{"object", "streaming"}, model.Branch)

	region := locationOf(t, "storage", "region/policy")
	require.True(t, region.Captures, "a kind may be discriminated twice")
	require.Equal(t, []string{"auto", "cdn", "failover"}, region.Branch)

	// A PLAIN SEGMENT IS NOT A BRANCH. `region/failover/main` is authored under a
	// literal, so there is nothing to choose and nothing to report.
	main := locationOf(t, "storage", "region/main")
	require.Empty(t, main.Branch)
	require.Equal(t, "region/failover/main", strings.Join(main.Authored, "/"))
}

// TestTHECODECCOMESFROMTHEVOCABULARY. A Duration and a Bytes both compile to a
// number, and reading one as the other hands a caller nanoseconds where it
// wanted a size.
func TestTheReaderIsChosenByTheDeclaredType(t *testing.T) {
	require.Equal(t, "Duration", locationOf(t, "storage", "streaming/ttl").Read)
	require.Equal(t, "Int", locationOf(t, "storage", "instances/limit").Read)
	require.Equal(t, "Bool", locationOf(t, "storage", "object/versioning").Read)
	require.Equal(t, "Strings", locationOf(t, "storage", "tags").Read)
	require.Equal(t, "String", locationOf(t, "storage", "name").Read)
}

// TestTHERENDEREREMITSWHATTHELOCATIONSSAY, against the accessor surface a
// compiled resource already answers with.
func TestTheRenderedReaderAddressesCompiledKeys(t *testing.T) {
	out := string(RenderReader("project", "storage", Locations(v2.GenerationRoot(), "storage")))

	require.Contains(t, out, `out.Size, _ = r.Int("size")`,
		"the hoisted field is read where it compiles")
	require.Contains(t, out, `out.ObjectVersioning, _ = r.Bool("object", "versioning")`,
		"a nested one is read where it nests")
	require.Contains(t, out, `out.StreamingTtl, _ = r.Duration("streaming", "ttl")`)
	require.Contains(t, out, `// authored at {object|streaming}/size`,
		"and the generated source says where the value came from")
	require.Contains(t, out, `// which of object, streaming was written`)
}
