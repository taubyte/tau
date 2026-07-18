package decompile

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/afero"
	compiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	"gotest.tools/v3/assert"
)

// TestDecompileRoundTripPreservesSubnet proves the DecompileDriver fixes the latent
// subnet-clobber bug in the old decompile/pass3/databases.go. A database authored
// with `network: subnet` compiles to `local=false` AND a surviving `network-access:
// subnet` wire key (subnet is not in the enum's DropWhen), so the ONLY faithful
// decompile keeps that preserved key. The old decompiler unconditionally rewrote
// `network-access` from the bool (`local=false` -> "all"), losing subnet and
// breaking the round-trip. The new driver's preserved-key-wins restore keeps it.
//
// A `network: host` database is included as the control: its wire key IS dropped at
// compile (host is in DropWhen), so it round-trips through the bool restore.
func TestDecompileRoundTripPreservesSubnet(t *testing.T) {
	const fixture = "testdata/subnet"

	compileFixture := func() compiler.Object {
		c, err := compiler.New(compiler.WithLocal(fixture), compiler.WithBranch("master"))
		assert.NilError(t, err)
		obj, _, err := c.Compile(context.Background())
		assert.NilError(t, err)
		return obj
	}

	// Compile, then decompile to an in-memory filesystem.
	obj := compileFixture()
	memFs := afero.NewMemMapFs()
	d, err := New(WithVirtual(memFs, "/"))
	assert.NilError(t, err)
	assert.NilError(t, d.Decompile(obj))

	// Concrete evidence of the fix: the decompiled YAML must carry the authored
	// `subnet`, not a clobbered `all`/`host`. (The db is keyed back by its name.)
	subnetYaml, err := afero.ReadFile(memFs, "/databases/test_db_subnet.yaml")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(subnetYaml), "network: subnet"),
		"decompiled subnet database must preserve `network: subnet`, got:\n%s", string(subnetYaml))

	// Full round-trip: recompiling the decompiled YAML must reproduce a fresh
	// compile exactly (subnet preserved, host restored via the bool).
	c2, err := compiler.New(compiler.WithVirtual(memFs, "/"), compiler.WithBranch("master"))
	assert.NilError(t, err)
	obj2, _, err := c2.Compile(context.Background())
	assert.NilError(t, err)

	fresh := compileFixture().Flat()["object"].(map[string]any)
	roundTripped := obj2.Flat()["object"].(map[string]any)
	assert.Assert(t, cmp.Equal(fresh, roundTripped), cmp.Diff(fresh, roundTripped))
}
