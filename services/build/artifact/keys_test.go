package artifact_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/artifact"
)

// spelled stands for a forge registry whose forge files paylib's clone URL
// under another key, the way a forge with an https path prefix does.
type spelled struct{ err error }

func (s spelled) Key(cloneURL string) (repokey.Key, error) {
	if s.err != nil {
		return repokey.Key{}, s.err
	}
	return repokey.Key{Host: "forge.example", Owner: "acme", Name: "paylib"}, nil
}

// A SOURCE IS KEYED THROUGH THE REGISTRY, so a push filed under the forge's
// canonical key builds the resource whichever address the config spelled.
func TestASourceIsKeyedTheWayTheForgeSpellsIt(t *testing.T) {
	e := &engine{}
	require.NoError(t, builder(t, deployed(t, oneEnvironment(t)), &checkout{dir: source(t)}, e, mainRepo,
		artifact.WithKeys(spelled{})).
		Build(t.Context(), work("forge.example/acme/paylib"), newOut()))
	require.Len(t, e.seen(), 1)

	e = &engine{}
	require.NoError(t, builder(t, deployed(t, oneEnvironment(t)), &checkout{dir: source(t)}, e, mainRepo,
		artifact.WithKeys(spelled{})).
		Build(t.Context(), work(paylibRepo), newOut()))
	require.Empty(t, e.seen(), "repokey's parse is not the key once a forge spells it otherwise")

	boom := errors.New("no such spelling")
	err := builder(t, deployed(t, oneEnvironment(t)), &checkout{dir: source(t)}, &engine{}, mainRepo,
		artifact.WithKeys(spelled{err: boom})).
		Build(t.Context(), work(paylibRepo), newOut())
	require.ErrorIs(t, err, boom)

	_, err = artifact.New(artifact.WithKeys(nil))
	require.Error(t, err)
}
