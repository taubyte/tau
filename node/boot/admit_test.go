package boot

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/p2p/sectls"
)

var testCA = sync.OnceValue(func() *sectls.CA {
	ca, err := sectls.NewCA("boot test", 24*time.Hour)
	if err != nil {
		panic(err)
	}
	return ca
})

// admit gives f an identity, when it has none, and that identity a certificate
// for f's cloud from the one CA every node in these tests shares.
func admit(t *testing.T, f File) File {
	t.Helper()
	return admitBy(t, testCA(), f, time.Hour)
}

func admitBy(t *testing.T, ca *sectls.CA, f File, validity time.Duration) File {
	t.Helper()
	raw, err := identity(f.PrivateKey)
	require.NoError(t, err)
	key, err := crypto.UnmarshalPrivateKey(raw)
	require.NoError(t, err)
	cert, err := ca.Issue(key.GetPublic(), f.Node.Cloud, validity)
	require.NoError(t, err)
	f.PrivateKey = base64.StdEncoding.EncodeToString(raw)
	f.CA = string(testCA().CertificatePEM())
	f.Certificate = string(cert)
	return f
}

func bare(t *testing.T) File {
	return File{
		Node:   node.Config{Services: []string{node.Register, node.Discovery}, Cloud: "acme"},
		Root:   t.TempDir(),
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
	}
}

func TestANodeTheFleetWouldRefuseRefusesToStart(t *testing.T) {
	other, err := sectls.NewCA("another", time.Hour)
	require.NoError(t, err)

	_, err = Start(t.Context(), admitBy(t, other, bare(t), time.Hour))
	require.ErrorIs(t, err, sectls.ErrChainInvalid, "a certificate from a CA this node does not trust")

	_, err = Start(t.Context(), admitBy(t, testCA(), bare(t), -time.Hour))
	require.ErrorIs(t, err, sectls.ErrExpired)

	stolen := admit(t, bare(t))
	stolen.PrivateKey = ""
	_, err = Start(t.Context(), stolen)
	require.ErrorIs(t, err, sectls.ErrPeerMismatch, "a certificate for another node's key")

	elsewhere := admit(t, bare(t))
	elsewhere.Node.Cloud = "elsewhere"
	_, err = Start(t.Context(), elsewhere)
	require.ErrorIs(t, err, sectls.ErrWrongCloud)

	garbled := admit(t, bare(t))
	garbled.CA = "not a certificate"
	_, err = Start(t.Context(), garbled)
	require.ErrorContains(t, err, "ca:")

	open := bare(t)
	open.Open, open.Certificate = true, "only half"
	_, err = Start(t.Context(), open)
	require.ErrorContains(t, err, "ca:")

	open.Certificate, open.PrivateKey, open.CA = "x", base64.StdEncoding.EncodeToString([]byte("not a key")), "y"
	_, err = Start(t.Context(), open)
	require.ErrorContains(t, err, "privatekey")

	_, err = Start(t.Context(), bare(t))
	require.ErrorIs(t, err, ErrNoCertificate)
}

func TestAPeerFromAnotherCAIsNotAdmitted(t *testing.T) {
	first, err := Start(t.Context(), admit(t, bare(t)))
	require.NoError(t, err)
	defer first.Close()
	target := libp2ppeer.AddrInfo{ID: first.Host().ID(), Addrs: first.Host().Addrs()}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	member, err := Start(t.Context(), admit(t, bare(t)))
	require.NoError(t, err)
	defer member.Close()
	require.NoError(t, member.Host().Connect(ctx, target))

	other, err := sectls.NewCA("another", time.Hour)
	require.NoError(t, err)
	strangerFile := admitBy(t, other, bare(t), time.Hour)
	strangerFile.CA = string(other.CertificatePEM())
	stranger, err := Start(t.Context(), strangerFile)
	require.NoError(t, err)
	defer stranger.Close()
	require.Error(t, stranger.Host().Connect(ctx, target), "the stranger dials in")
	require.Error(t, first.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: stranger.Host().ID(), Addrs: stranger.Host().Addrs()}), "and is dialled")

	openFile := bare(t)
	openFile.Open = true
	open, err := Start(t.Context(), openFile)
	require.NoError(t, err, "open: true starts with no certificate")
	defer open.Close()

	// A TLS 1.3 client learns that the server refused it on its first read,
	// so an open node's dial can return before the connection is dropped.
	open.Host().Connect(ctx, target)
	require.Eventually(t, func() bool {
		return open.Host().Network().Connectedness(first.Host().ID()) != network.Connected
	}, 5*time.Second, 50*time.Millisecond, "an open node is no member")
	require.NotEqual(t, network.Connected, first.Host().Network().Connectedness(open.Host().ID()))
	require.Error(t, first.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: open.Host().ID(), Addrs: open.Host().Addrs()}))
}
