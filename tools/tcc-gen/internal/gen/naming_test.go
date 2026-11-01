package gen

import (
	"testing"

	"github.com/stretchr/testify/require"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// Covers a Field() override and a hyphenated path, which the generator handles and v2
// does not use, so the code is still exercised.

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

func TestTheWireKeyIsWhatTheObjectCarries(t *testing.T) {
	require.Equal(t, "timeout", wireKey(Field{Name: "Timeout"}))
}

func TestAMatcherIsNotALocation(t *testing.T) {
	either := engine.String("size", engine.Path(engine.Either("object", "streaming"), "size"))
	_, ok := pathSegs(either)
	require.False(t, ok)

	segs, ok := pathSegs(engine.String("timeout"))
	require.True(t, ok)
	require.Equal(t, []string{"timeout"}, segs)
}
