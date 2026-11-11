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
// before the peer answers, sends, and reads the refusal. Whether a reset would have
// overtaken the refusal is timing, so it is asked many times.
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
		if err == nil {
			s.Write([]byte("a request sent before the answer"))
			s.CloseWrite()
			_, err = s.Read(make([]byte, 1))
			s.Reset()
		}
		require.ErrorIs(t, err, msmux.ErrNotSupported[protocol.ID]{}, "attempt %d", attempt)
	}
}
