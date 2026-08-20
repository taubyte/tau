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

// RequiredWhen naming a Captures() sibling used to be a promise the loader did not keep:
// the condition read the sibling's document VALUE, and a Key's path ends in the
// Either() where the document holds the branch, so the comparison never matched and
// the field was never required. `engine/distributed/size` said RequiredWhen and a
// database with no size compiled clean.
func TestRequiredWhenReadsAKeysBranch(t *testing.T) {
	schema := SchemaDefinition(Root(
		Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required()),
			String("kind", Path("engine", Either("local", "remote")), Captures()),
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

// An Either()+Captures() switch on an OPTIONAL block. Absent and wrong are different
// documents and were getting the same answer, which is why every switch in the tau
// vocabulary sits on a block that must be there — not by design, but because the
// others could not be written.
func TestKeySwitchOnAnOptionalBlock(t *testing.T) {
	schema := SchemaDefinition(Root(
		Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required()),
			// optional: hangs off a `probe` block that need not be there
			String("probe", Path("probe", Either("http", "function")), Captures()),
			String("probe-path", Path("probe", "http", "path")),
			// mandatory: the switch is at the top level, so its parent is the document
			String("shape", Path(Either("round", "square")), Captures()),
		})),
	))

	for _, tc := range []struct{ name, doc, wantErr, wantProbe string }{
		{"optional block absent", "id: one\nround: {}\n", "", ""},
		{"optional block present", "id: one\nround: {}\nprobe:\n    http:\n        path: /x\n", "", "http"},
		{"optional block present, unlisted key", "id: one\nround: {}\nprobe:\n    telepathy: {}\n", "empty key", ""},
		{"top-level switch, nothing matched", "id: one\n", "empty key", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("id: root\n"), 0o644))
			assert.NilError(t, afero.WriteFile(fs, "/things/one.yaml", []byte(tc.doc), 0o644))
			e, err := New(schema, yaseer.VirtualFS(fs, "/"))
			assert.NilError(t, err)
			obj, err := e.Process()
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			assert.NilError(t, err)
			things, err := obj.Child("things").Object()
			assert.NilError(t, err)
			thing, err := things.Child("one").Object()
			assert.NilError(t, err)
			got := thing.Get("probe")
			if tc.wantProbe == "" {
				assert.Assert(t, got == nil, "an absent block leaves the key unset, not empty; got %v", got)
				return
			}
			assert.Equal(t, got, tc.wantProbe)
		})
	}
}

// A group names the document its instances keep their own fields in. Before, that name
// was a package-level global, so it was reserved EVERYWHERE: a nested group called
// `config` under a resource was skipped in silence, because somewhere else in the tree
// a container keeps its fields in a file by that name.
func TestAGroupNamesItsOwnDocument(t *testing.T) {
	schema := SchemaDefinition(Root(
		Attributes(String("id")),
		DefineGroup("scopes",
			DefineIterGroup([]*Attribute{String("id", Required()), String("owner")},
				DefineGroup("things", DefineIter([]*Attribute{
					String("id", Required()),
				}, Nested(DefineGroup("config", DefineIter([]*Attribute{String("value")}))))),
			).With(Leaf("scope")),
		),
	))

	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("id: root\n"), 0o644))
	// the scope's own fields live in scope.yaml, exactly as the DSL says
	assert.NilError(t, afero.WriteFile(fs, "/scopes/a/scope.yaml", []byte("id: a\nowner: me\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/scopes/a/things/t.yaml",
		[]byte("id: t\nconfig:\n    LOG_LEVEL:\n        value: info\n"), 0o644))

	e, err := New(schema, yaseer.VirtualFS(fs, "/"))
	assert.NilError(t, err)
	obj, err := e.Process()
	assert.NilError(t, err)

	scopes, err := obj.Child("scopes").Object()
	assert.NilError(t, err)
	a, err := scopes.Child("a").Object()
	assert.NilError(t, err)
	assert.Equal(t, a.Get("owner"), "me", "the scope read its own fields from scope.yaml")

	things, err := a.Child("things").Object()
	assert.NilError(t, err)
	tObj, err := things.Child("t").Object()
	assert.NilError(t, err)
	cfg, err := tObj.Child("config").Object()
	assert.NilError(t, err, "a nested group named config used to vanish")
	entry, err := cfg.Child("LOG_LEVEL").Object()
	assert.NilError(t, err)
	assert.Equal(t, entry.Get("value"), "info")
}
