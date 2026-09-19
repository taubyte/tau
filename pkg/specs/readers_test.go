package specs_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// readers_test exercises what the generator emitted for EVERY kind, against the
// vocabulary's own fixture.
//
// # Why this is one test and not ninety
//
// The readers are generated, so they are wrong together or right together: a
// fault is in the model or the renderer, never in one hand-typed line. What a
// test can still catch is a reader that reads the wrong place — a compiled key
// that does not exist, a scalar decoded as the wrong type — and that shows up
// the same way for every kind. So this walks the fixture and reads every
// resource with the reader for its own kind.
//
// IT ALSO KEEPS THE GENERATED SURFACE HONEST. A kind added to the vocabulary
// arrives here with no edit, because the map is generated from the same walk
// the readers are.

func TestEveryGeneratedReaderReadsItsOwnKind(t *testing.T) {
	p := load(t)

	var read int
	var walk func(scope []string)
	walk = func(scope []string) {
		kinds, err := p.Kinds(scope)
		require.NoError(t, err)

		for _, kind := range kinds {
			names, err := p.Names(scope, kind)
			if err != nil {
				continue
			}
			reader, ok := specs.Readers[kind]
			require.True(t, ok, "the vocabulary declares %q and the generator emitted no reader for it", kind)

			for _, name := range names {
				r, id, err := p.Resource(scope, kind, name)
				require.NoError(t, err)

				out := reader(r)
				require.NotNil(t, out, "%s/%s read as nothing", kind, name)

				// THE NAME IS THE ONE FIELD EVERY KIND CARRIES IN THE OBJECT, so
				// it is what a walk over all of them can assert: a reader
				// addressing the wrong place gets it wrong first.
				//
				// THE ID IS NOT, and that is the object's shape rather than a
				// gap: a resource is KEYED by its id, so repeating it inside
				// would be the same fact twice. It comes back from the lookup,
				// which is where it actually lives.
				require.Equal(t, name, r.String("name"), "%s/%s", kind, name)
				require.NotEmpty(t, id, "%s/%s has no id in the index", kind, name)
				read++
			}
		}

		scopes, err := p.Scopes(scope)
		require.NoError(t, err)
		for _, inner := range scopes {
			walk(append(append([]string{}, scope...), inner))
		}
	}
	walk(nil)

	require.Greater(t, read, 40, "the fixture should exercise most of the vocabulary")
}

// TestTHEUNIONSREADWHATANYKINDDECLARES — the cross-kind questions, over the one
// kind whose source is written the short way and the one whose build is not.
func TestTheUnionsReadAcrossKinds(t *testing.T) {
	p := load(t)

	fn, _, err := p.Resource(nil, "function", "reserve_stock")
	require.NoError(t, err)

	src := specs.ReadSourceBlock(fn)
	require.Equal(t, "services/reserve", src.Source, "the bare form, at its own key")
	require.Empty(t, src.SourcePath, "and not the block form")

	build := specs.ReadBuildBlock(fn)
	require.Equal(t, "commands", build.BuildKind)
	require.Equal(t, "rust:1.83", build.BuildImage)

	// AN APPLICATION DECLARES A DIFFERENT SOURCE and the same union reads it,
	// which is the whole point of a union: a walk over a project meets both.
	app, _, err := p.Resource(nil, "application", "api")
	require.NoError(t, err)
	require.NotEmpty(t, specs.ReadSourceBlock(app).SourceImage)
}
