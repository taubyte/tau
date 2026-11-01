package assets_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/assets"
	"github.com/taubyte/tau/services/build/backend"
)

// It is the seam the build backend names, and satisfying that interface is the
// whole point of the package.
var _ backend.Assets = (*assets.Store)(nil)

func store(t *testing.T) (*assets.Store, peer.Node) {
	t.Helper()

	node := peer.Mock(t.Context())
	t.Cleanup(node.Close)

	s, err := assets.New(node)
	require.NoError(t, err)
	return s, node
}

func TestContentIsItsOwnAddress(t *testing.T) {
	s, _ := store(t)

	first, err := s.Put(t.Context(), strings.NewReader("cloning\ncompiling\n"))
	require.NoError(t, err)
	require.NotEmpty(t, first)

	again, err := s.Put(t.Context(), strings.NewReader("cloning\ncompiling\n"))
	require.NoError(t, err)
	require.Equal(t, first, again)

	other, err := s.Put(t.Context(), strings.NewReader("something else\n"))
	require.NoError(t, err)
	require.NotEqual(t, first, other)
}

func TestWhatGoesInComesBack(t *testing.T) {
	s, _ := store(t)

	// No length is known in advance.
	content := bytes.Repeat([]byte("a line of build output\n"), 4096)

	id, err := s.Put(t.Context(), bytes.NewReader(content))
	require.NoError(t, err)

	r, err := s.Get(t.Context(), id)
	require.NoError(t, err)
	defer r.Close()

	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestAReaderThatBreaks(t *testing.T) {
	s, _ := store(t)

	_, err := s.Put(t.Context(), io.MultiReader(
		strings.NewReader("the first part\n"),
		brokenReader{},
	))
	require.ErrorContains(t, err, "the pipe went away")
}

func TestWhatItRefuses(t *testing.T) {
	s, _ := store(t)

	_, err := assets.New(nil)
	require.ErrorIs(t, err, assets.ErrNoNode)

	_, err = s.Put(t.Context(), nil)
	require.ErrorContains(t, err, "nothing to store")

	_, err = s.Get(t.Context(), "")
	require.ErrorContains(t, err, "no content id")

	_, err = s.Get(t.Context(), "not-a-cid")
	require.Error(t, err)
}

func TestAContextThatHasAlreadyEnded(t *testing.T) {
	s, _ := store(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := s.Put(ctx, strings.NewReader("output\n"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestConcurrentPuts(t *testing.T) {
	s, _ := store(t)

	ids := make(chan string, 16)
	for i := range 16 {
		go func() {
			id, err := s.Put(t.Context(), strings.NewReader(strings.Repeat("x", i+1)))
			if err != nil {
				ids <- ""
				return
			}
			ids <- id
		}()
	}

	seen := map[string]bool{}
	for range 16 {
		id := <-ids
		require.NotEmpty(t, id)
		require.False(t, seen[id], "sixteen different contents produced a repeated id")
		seen[id] = true
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("the pipe went away") }
