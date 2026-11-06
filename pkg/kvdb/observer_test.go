package kvdb

import (
	"sync"
	"testing"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/peer"
)

// heard is an observer that keeps what it was told.
type heard struct {
	mu      sync.Mutex
	put     map[string]string
	deleted map[string]bool
}

func newHeard() *heard { return &heard{put: map[string]string{}, deleted: map[string]bool{}} }

func (h *heard) Put(key string, value []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.put[key] = string(value)
	delete(h.deleted, key)
}

func (h *heard) Delete(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.deleted[key] = true
}

func (h *heard) value(key string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.put[key]
	return v, ok
}

func (h *heard) gone(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.deleted[key]
}

func TestAnObserverHearsWhatThisNodeWritesAndWhatItMerges(t *testing.T) {
	writer, reader := peer.Mock(t.Context()), peer.Mock(t.Context())
	t.Cleanup(func() { writer.Close(); reader.Close() })
	require.NoError(t, peer.LinkAllPeers())
	require.NoError(t, reader.Host().Connect(t.Context(), libp2ppeer.AddrInfo{ID: writer.ID(), Addrs: writer.Host().Addrs()}))

	here := newHeard()
	there, err := Open(writer, "observed")
	require.NoError(t, err)
	t.Cleanup(func() { there.Close() })
	mine, err := Open(reader, "observed", WithObserver(here))
	require.NoError(t, err)
	t.Cleanup(func() { mine.Close() })

	require.NoError(t, mine.Put(t.Context(), "h/local", []byte("written here")))
	got, ok := here.value("h/local")
	require.True(t, ok, "a write on this node is heard, keyed as it was written")
	require.Equal(t, "written here", got)

	require.NoError(t, there.Put(t.Context(), "h/remote", []byte("written there")))
	require.Eventually(t, func() bool {
		got, ok := here.value("h/remote")
		return ok && got == "written there"
	}, 30*time.Second, 50*time.Millisecond, "a write merged from another node is heard too")

	require.NoError(t, mine.Delete(t.Context(), "h/local"))
	require.True(t, here.gone("h/local"))
}
