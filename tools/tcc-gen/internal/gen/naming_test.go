package gen

import (
	"testing"

	"github.com/stretchr/testify/require"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// naming_test covers the engine features the CURRENT vocabulary happens not to
// use: a Field() override, a hyphenated path, a mapstructure Tag() and a
// Compat() alias. The generator handles all four, and v2 declares none of them —
// so without this they are handled by code nothing ever runs, and a vocabulary
// that started using one tomorrow would find out from a broken console.

func TestAFieldsGoNameComesFromWhereItWasWritten(t *testing.T) {
	for _, tc := range []struct {
		name string
		attr *engine.Attribute
		want string
	}{
		{"its own name", engine.String("timeout"), "Timeout"},
		{"the last path segment", engine.String("kind", engine.Path("serve", "kind")), "Kind"},
		{"a nested key names itself by its last level too", engine.String(engine.Key("serve", "kind")), "Kind"},

		// A KEY IS ONLY SPELLED OUT WHERE THE PATH CANNOT BE. `route/handler/kind`
		// is authored under a matcher, so there is no single authored place whose
		// last segment could name it — and the key's own segments, each a bare
		// identifier by construction, are what is left.
		{"a key under a matcher", engine.String(engine.Key("route", "handler", "kind"),
			engine.Path("route", "handler", engine.Either("function", "website"), "kind")), "RouteHandlerKind"},
		{"a hyphen is a word break", engine.String("git-provider"), "GitProvider"},
		{"an override wins", engine.String("kind", engine.Field("HandlerKind")), "HandlerKind"},
		{"and so does an accessor name", engine.String("kind", engine.Accessor("Protocol")), "Protocol"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, structFieldName("function", tc.attr))
		})
	}
}

// TestTHEWIREKEYISWHATTHEOBJECTCARRIES. A struct field is named for the author;
// the compiled object is keyed for the compiler, and where a tag or a compat
// alias says they differ, the consumer decoding the object needs the second one.
func TestTheWireKeyIsWhatTheObjectCarries(t *testing.T) {
	plain := engine.String("timeout")
	require.Empty(t, structTag("Timeout", plain))
	require.Equal(t, "timeout", wireKey(Field{Name: "Timeout"}))

	// A TAG THAT ONLY RESTATES THE NAME IS NOT ONE: mapstructure decodes
	// case-insensitively, so `mapstructure:"timeout"` on Timeout is noise.
	same := engine.String("timeout", engine.Tag("timeout"))
	require.Empty(t, structTag("Timeout", same))

	tagged := engine.String("kind", engine.Tag("handler_kind"))
	require.Equal(t, "`mapstructure:\"handler_kind\"`", structTag("Kind", tagged))
	require.Equal(t, "handler_kind", wireKey(Field{Name: "Kind", Tag: structTag("Kind", tagged)}))

	// A COMPAT ALIAS IS A WIRE KEY TOO. The engine reads it when the canonical
	// path is absent, so a decoder that did not know it would read nothing from
	// an object written before the rename.
	alias := engine.String("regex", engine.Path("regex"), engine.Compat("useRegex"))
	require.Equal(t, "`mapstructure:\"useRegex\"`", structTag("Regex", alias))
	require.Equal(t, "useRegex", wireKey(Field{Name: "Regex", Tag: structTag("Regex", alias)}))

	// ...unless it is the field name again, which is a rename that renamed
	// nothing.
	noop := engine.String("regex", engine.Path("regex"), engine.Compat("regex"))
	require.Empty(t, structTag("Regex", noop))
}

// TestAMatcherIsNotALocation. A path with a matcher in it does not name one
// place, so nothing that needs a single key can use it.
func TestAMatcherIsNotALocation(t *testing.T) {
	either := engine.String("size", engine.Path(engine.Either("object", "streaming"), "size"))
	_, ok := pathSegs(either)
	require.False(t, ok)

	segs, ok := pathSegs(engine.String("timeout"))
	require.True(t, ok)
	require.Equal(t, []string{"timeout"}, segs)

	_, ok = compatSegs(engine.String("timeout"))
	require.False(t, ok, "an attribute with no Compat() has no alias")
}
