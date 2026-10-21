package sectls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/core/sec"
	tptu "github.com/libp2p/go-libp2p/p2p/net/upgrader"
	libp2ptls "github.com/libp2p/go-libp2p/p2p/security/tls"
	"github.com/stretchr/testify/require"
)

const cloud = "example.cloud"

type member struct {
	key  ic.PrivKey
	id   peer.ID
	cert []byte
}

func newCA(t *testing.T) *CA {
	t.Helper()
	ca, err := NewCA("test", time.Hour)
	require.NoError(t, err)
	return ca
}

func join(t *testing.T, ca *CA, cloudName string, validity time.Duration) member {
	t.Helper()
	key, pub, err := ic.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	id, err := peer.IDFromPublicKey(pub)
	require.NoError(t, err)
	certPEM, err := ca.Issue(pub, cloudName, validity)
	require.NoError(t, err)
	return member{key: key, id: id, cert: certPEM}
}

func (m member) credentials(t *testing.T, ca *CA) *Credentials {
	t.Helper()
	cert, err := ParseCertificate(m.cert)
	require.NoError(t, err)
	return &Credentials{Root: ca.Root, Cert: cert.Raw, Cloud: cloud}
}

func transport(t *testing.T, m member, creds *Credentials) *Transport {
	t.Helper()
	tr, err := New(creds)(ID, m.key, []tptu.StreamMuxer{{ID: protocol.ID("/yamux/1.0.0")}})
	require.NoError(t, err)
	return tr
}

func handshake(t *testing.T, server, client *Transport, serverID peer.ID) (sec.SecureConn, error, error) {
	t.Helper()
	a, b := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		conn sec.SecureConn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		c, err := client.SecureOutbound(ctx, a, serverID)
		if err == nil {
			// TLS 1.3 reports the server's refusal on the first read.
			buf := make([]byte, 1)
			_, err = c.Read(buf)
			if errors.Is(err, io.EOF) {
				err = nil
			}
		}
		done <- result{c, err}
	}()
	s, serr := server.SecureInbound(ctx, b, "")
	if s != nil {
		s.Close()
	}
	r := <-done
	if r.conn != nil {
		r.conn.Close()
	}
	return s, serr, r.err
}

func TestHandshakeAdmitsBothDirections(t *testing.T) {
	ca := newCA(t)
	s, c := join(t, ca, cloud, time.Hour), join(t, ca, cloud, time.Hour)
	server, client := transport(t, s, s.credentials(t, ca)), transport(t, c, c.credentials(t, ca))
	require.Equal(t, protocol.ID(ID), server.ID())

	conn, serr, cerr := handshake(t, server, client, s.id)
	require.NoError(t, serr)
	require.NoError(t, cerr)
	require.Equal(t, c.id, conn.RemotePeer())
	require.Equal(t, s.id, conn.LocalPeer())
	require.NotNil(t, conn.RemotePublicKey())
	require.Equal(t, protocol.ID("/yamux/1.0.0"), conn.ConnState().StreamMultiplexer)
}

func TestHandshakeRefuses(t *testing.T) {
	ca, other := newCA(t), newCA(t)

	cases := map[string]func(s, c *member){
		"wrong CA":       func(_, c *member) { *c = join(t, other, cloud, time.Hour) },
		"wrong cloud":    func(_, c *member) { *c = join(t, ca, "other.cloud", time.Hour) },
		"expired":        func(_, c *member) { *c = join(t, ca, cloud, -time.Hour) },
		"another's cert": func(s, c *member) { c.cert = s.cert },
		"no certificate": func(_, c *member) { c.cert = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, c := join(t, ca, cloud, time.Hour), join(t, ca, cloud, time.Hour)
			mutate(&s, &c)
			var ccreds *Credentials
			if c.cert == nil {
				ccreds = &Credentials{Root: ca.Root, Cloud: cloud}
			} else {
				ccreds = c.credentials(t, ca)
			}
			server, client := transport(t, s, s.credentials(t, ca)), transport(t, c, ccreds)
			_, serr, _ := handshake(t, server, client, s.id)
			require.Error(t, serr, "the server must refuse the client")

			// And the other way: the bad peer as server is refused by a good client.
			server, client = transport(t, c, ccreds), transport(t, s, s.credentials(t, ca))
			_, _, cerr := handshake(t, server, client, c.id)
			require.Error(t, cerr, "the client must refuse the server")
		})
	}
}

func TestVerifyReasons(t *testing.T) {
	ca, other := newCA(t), newCA(t)
	good := join(t, ca, cloud, time.Hour)
	creds := good.credentials(t, ca)
	der := func(m member) []byte {
		c, err := ParseCertificate(m.cert)
		require.NoError(t, err)
		return c.Raw
	}

	require.NoError(t, creds.Verify(der(good), good.key.GetPublic()))
	require.ErrorIs(t, creds.Verify(nil, good.key.GetPublic()), ErrNoCertificate)
	require.Error(t, creds.Verify([]byte("junk"), good.key.GetPublic()))

	foreign := join(t, other, cloud, time.Hour)
	require.ErrorIs(t, creds.Verify(der(foreign), foreign.key.GetPublic()), ErrChainInvalid)

	stale := join(t, ca, cloud, -time.Hour)
	require.ErrorIs(t, creds.Verify(der(stale), stale.key.GetPublic()), ErrExpired)

	early := &Credentials{Root: ca.Root, Cloud: cloud, Now: func() time.Time { return time.Now().Add(-time.Hour) }}
	require.ErrorIs(t, early.Verify(der(good), good.key.GetPublic()), ErrExpired)

	someone := join(t, ca, cloud, time.Hour)
	require.ErrorIs(t, creds.Verify(der(good), someone.key.GetPublic()), ErrPeerMismatch)

	_, secp, err := ic.GenerateSecp256k1Key(rand.Reader)
	require.NoError(t, err)
	require.ErrorIs(t, creds.Verify(der(good), secp), ErrPeerMismatch)

	elsewhere := join(t, ca, "other.cloud", time.Hour)
	require.ErrorIs(t, creds.Verify(der(elsewhere), elsewhere.key.GetPublic()), ErrWrongCloud)

	// A certificate the same CA issued for something other than a node.
	std, err := ic.PubKeyToStdKey(good.key.GetPublic())
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{Organization: []string{cloud}},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
	raw, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Root, std, ca.key)
	require.NoError(t, err)
	require.ErrorIs(t, creds.Verify(raw, good.key.GetPublic()), ErrWrongPurpose)
}

func TestNewCredentials(t *testing.T) {
	ca := newCA(t)
	m := join(t, ca, cloud, time.Hour)

	creds, err := NewCredentials(ca.CertificatePEM(), m.cert, cloud, m.key)
	require.NoError(t, err)
	require.Equal(t, cloud, creds.Cloud)

	_, err = NewCredentials([]byte("x"), m.cert, cloud, m.key)
	require.ErrorContains(t, err, "ca:")
	_, err = NewCredentials(ca.CertificatePEM(), []byte("x"), cloud, m.key)
	require.ErrorContains(t, err, "certificate:")

	stranger := join(t, ca, cloud, time.Hour)
	_, err = NewCredentials(ca.CertificatePEM(), m.cert, cloud, stranger.key)
	require.ErrorIs(t, err, ErrPeerMismatch)

	stale := join(t, ca, cloud, -time.Hour)
	_, err = NewCredentials(ca.CertificatePEM(), stale.cert, cloud, stale.key)
	require.ErrorIs(t, err, ErrExpired)
}

func TestLoadCA(t *testing.T) {
	ca := newCA(t)
	keyPEM, err := ca.KeyPEM()
	require.NoError(t, err)

	loaded, err := LoadCA(ca.CertificatePEM(), keyPEM)
	require.NoError(t, err)
	m := join(t, loaded, cloud, time.Hour)
	_, err = NewCredentials(ca.CertificatePEM(), m.cert, cloud, m.key)
	require.NoError(t, err)

	_, err = LoadCA([]byte("x"), keyPEM)
	require.Error(t, err)
	_, err = LoadCA(ca.CertificatePEM(), []byte("x"))
	require.ErrorContains(t, err, "not PEM")
	_, err = LoadCA(ca.CertificatePEM(), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("x")}))
	require.ErrorContains(t, err, "CA key")

	otherKey, err := newCA(t).KeyPEM()
	require.NoError(t, err)
	_, err = LoadCA(ca.CertificatePEM(), otherKey)
	require.ErrorContains(t, err, "does not match")

	// A user's own CA need not be Ed25519.
	ek, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "user"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &ek.PublicKey, ek)
	require.NoError(t, err)
	pk, err := x509.MarshalPKCS8PrivateKey(ek)
	require.NoError(t, err)
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	user, err := LoadCA(rootPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))
	require.NoError(t, err)
	m = join(t, user, cloud, time.Hour)
	_, err = NewCredentials(rootPEM, m.cert, cloud, m.key)
	require.NoError(t, err)
}

func TestIssueRefusesUnusableKey(t *testing.T) {
	_, secp, err := ic.GenerateSecp256k1Key(rand.Reader)
	require.NoError(t, err)
	_, err = newCA(t).Issue(secp, cloud, time.Hour)
	require.Error(t, err)
}

func TestParseCertificateRefusesOtherBlocks(t *testing.T) {
	_, err := ParseCertificate(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("x")}))
	require.Error(t, err)
}

func TestHostsConnectOnlyWithCertificates(t *testing.T) {
	ca, other := newCA(t), newCA(t)
	a, b, c := join(t, ca, cloud, time.Hour), join(t, ca, cloud, time.Hour), join(t, other, cloud, time.Hour)
	ha, err := libp2p.New(libp2p.Identity(a.key), libp2p.Security(ID, New(a.credentials(t, ca))), libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer ha.Close()

	hb, err := libp2p.New(libp2p.Identity(b.key), libp2p.Security(ID, New(b.credentials(t, ca))), libp2p.NoListenAddrs)
	require.NoError(t, err)
	defer hb.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, hb.Connect(ctx, peer.AddrInfo{ID: ha.ID(), Addrs: ha.Addrs()}))

	ccreds := c.credentials(t, other)
	hc, err := libp2p.New(libp2p.Identity(c.key), libp2p.Security(ID, New(ccreds)), libp2p.NoListenAddrs)
	require.NoError(t, err)
	defer hc.Close()
	require.Error(t, hc.Connect(ctx, peer.AddrInfo{ID: ha.ID(), Addrs: ha.Addrs()}))
}

func TestHandshakeRefusesTheWrongPeer(t *testing.T) {
	ca := newCA(t)
	s, c, x := join(t, ca, cloud, time.Hour), join(t, ca, cloud, time.Hour), join(t, ca, cloud, time.Hour)
	server, client := transport(t, s, s.credentials(t, ca)), transport(t, c, c.credentials(t, ca))
	_, _, cerr := handshake(t, server, client, x.id)
	var mismatch sec.ErrPeerIDMismatch
	require.ErrorAs(t, cerr, &mismatch)
}

func TestHandshakeRefusesStockTLS(t *testing.T) {
	ca := newCA(t)
	s, c := join(t, ca, cloud, time.Hour), join(t, ca, cloud, time.Hour)
	server := transport(t, s, s.credentials(t, ca))
	stock, err := libp2ptls.New(ID, c.key, nil)
	require.NoError(t, err)

	a, b := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		conn, err := stock.SecureOutbound(ctx, a, s.id)
		if err == nil {
			conn.Read(make([]byte, 1))
			conn.Close()
		}
	}()
	_, err = server.SecureInbound(ctx, b, "")
	require.ErrorIs(t, err, ErrNoCertificate)
}

func TestHandshakeWithoutMuxers(t *testing.T) {
	ca := newCA(t)
	s, c := join(t, ca, cloud, time.Hour), join(t, ca, cloud, time.Hour)
	bare := func(m member) *Transport {
		tr, err := New(m.credentials(t, ca))(ID, m.key, nil)
		require.NoError(t, err)
		return tr
	}
	conn, serr, cerr := handshake(t, bare(s), bare(c), s.id)
	require.NoError(t, serr)
	require.NoError(t, cerr)
	require.Empty(t, conn.ConnState().StreamMultiplexer)
}
