package peer

import (
	"testing"

	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	msmux "github.com/multiformats/go-multistream"
	"github.com/stretchr/testify/require"
)

// A PROTOCOL A MOCK PEER DOES NOT SERVE is refused in words its caller reads, as a
// real transport's reset code says it: a bare reset is also how a call that reached
// its service can end. Told the peer serves it, the caller is handed the stream
// before the peer answers and, as HTTP/2 does, sends on while it reads; no send may
// fail before the refusal is read. Which would come first is timing, so it is asked
// many times.
func TestAMockPeerRefusesAProtocolItDoesNotServeInWordsItsCallerReads(t *testing.T) {
	net := testNet(t)
	a, b := net.Mock(t.Context()), net.Mock(t.Context())
	defer a.Close()
	defer b.Close()
	require.NoError(t, net.LinkAll())
	require.NoError(t, a.Host().Connect(t.Context(), peercore.AddrInfo{ID: b.ID(), Addrs: b.Host().Addrs()}))

	const unserved = protocol.ID("/tau/test/unserved")
	for attempt := range 200 {
		require.NoError(t, a.Host().Peerstore().AddProtocols(b.ID(), unserved))
		s, err := a.Host().NewStream(t.Context(), b.ID(), unserved)
		if err != nil {
			require.ErrorIs(t, err, msmux.ErrNotSupported[protocol.ID]{}, "attempt %d", attempt)
			continue
		}
		read := make(chan error, 1)
		go func() {
			_, err := s.Read(make([]byte, 1))
			read <- err
		}()
		for range 64 {
			_, err := s.Write(make([]byte, 1<<10))
			require.NoError(t, err, "attempt %d: a send failed before the refusal was read", attempt)
		}
		require.ErrorIs(t, <-read, msmux.ErrNotSupported[protocol.ID]{}, "attempt %d", attempt)
		s.Reset()
	}
}
