package specs_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// The readers are generated, so they are right or wrong together. This walks the
// fixture and reads every resource with its own kind's reader; a new kind arrives
// here with no edit.

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

				// The name is the one field every kind carries in the object. The id is not: a
				// resource is keyed by it, and it comes back from the lookup.
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
