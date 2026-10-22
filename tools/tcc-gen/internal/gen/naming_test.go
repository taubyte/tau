package gen

import (
	"testing"

	"github.com/stretchr/testify/require"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// naming_test covers the engine features the CURRENT vocabulary happens not to
// use: a Field() override and a hyphenated path. The generator handles both, and
// v2 declares neither — so without this they are handled by code nothing ever runs, and a vocabulary
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

// TestTheWireKeyIsWhatTheObjectCarries. A struct field decodes from the
// lower-cased name the compiler writes.
func TestTheWireKeyIsWhatTheObjectCarries(t *testing.T) {
	require.Equal(t, "timeout", wireKey(Field{Name: "Timeout"}))
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
}
