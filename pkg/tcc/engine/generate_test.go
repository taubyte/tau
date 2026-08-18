package engine

import (
	"strings"
	"testing"

	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

// Derive is a pure function: same input, same output, on every machine, forever.
// Generate is the opposite by contract, and confusing the two is a real bug in either
// direction — a minted id that reproduced would collide, a derived name that did not
// would resolve differently in two consumers.
func TestDeriveIsPureAndGenerateIsNot(t *testing.T) {
	const id = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"

	a, err := Derive(DeriveDNSLabel, id)
	assert.NilError(t, err)
	b, err := Derive(DeriveDNSLabel, id)
	assert.NilError(t, err)
	assert.Equal(t, a, b, "a derivation that is not reproducible is not a derivation")

	g1, err := Generate(GenCID, id)
	assert.NilError(t, err)
	g2, err := Generate(GenCID, id)
	assert.NilError(t, err)
	assert.Assert(t, g1 != g2, "a generator that reproduces its output would mint colliding ids")
}

// A DNS label is case-insensitive, so the alphabet has to be too, and it may hold
// only letters and digits.
func TestDeriveDNSLabelShape(t *testing.T) {
	label, err := Derive(DeriveDNSLabel, "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR")
	assert.NilError(t, err)
	assert.Equal(t, len(label), dnsLabelLength)
	assert.Equal(t, label, strings.ToLower(label), "case cannot carry information in DNS")
	for _, r := range label {
		ok := (r >= 'a' && r <= 'z') || (r >= '2' && r <= '7')
		assert.Assert(t, ok, "label %q has %q, which base32 does not produce", label, r)
	}

	_, err = Derive(DeriveDNSLabel)
	assert.ErrorContains(t, err, "needs an identifier")
	_, err = Derive("no-such-derivation", "x")
	assert.ErrorContains(t, err, "unknown derivation")
}

// RequiredWhen naming a Key() sibling used to be a promise the loader did not keep:
// the condition read the sibling's document VALUE, and a Key's path ends in the
// Either() where the document holds the branch, so the comparison never matched and
// the field was never required. `engine/distributed/size` said RequiredWhen and a
// database with no size compiled clean.
func TestRequiredWhenReadsAKeysBranch(t *testing.T) {
	schema := SchemaDefinition(Root(
		Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required()),
			String("kind", Path("engine", Either("local", "remote")), Key()),
			String("host", Path("engine", "remote", "host"), RequiredWhen("kind", "remote")),
		})),
	))

	for _, tc := range []struct{ name, doc, wantErr string }{
		{"branch that requires it, missing", "id: one\nengine:\n    remote: {}\n", "required attribute 'engine/remote/host'"},
		{"branch that requires it, present", "id: one\nengine:\n    remote:\n        host: h\n", ""},
		{"the other branch", "id: one\nengine:\n    local: {}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("id: root\n"), 0o644))
			assert.NilError(t, afero.WriteFile(fs, "/things/one.yaml", []byte(tc.doc), 0o644))
			e, err := New(schema, yaseer.VirtualFS(fs, "/"))
			assert.NilError(t, err)
			_, err = e.Process()
			if tc.wantErr == "" {
				assert.NilError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}
