package sectls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime/debug"
	"time"

	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/core/sec"
	tptu "github.com/libp2p/go-libp2p/p2p/net/upgrader"
	libp2ptls "github.com/libp2p/go-libp2p/p2p/security/tls"
)

const ID = libp2ptls.ID

const certValidityPeriod = 100 * 365 * 24 * time.Hour

type Transport struct {
	identity   *libp2ptls.Identity
	creds      *Credentials
	localPeer  peer.ID
	muxers     []protocol.ID
	protocolID protocol.ID
}

var _ sec.SecureTransport = &Transport{}

// New is a libp2p security constructor: libp2p.Security(sectls.ID, sectls.New(creds)).
func New(creds *Credentials) func(id protocol.ID, key ic.PrivKey, muxers []tptu.StreamMuxer) (*Transport, error) {
	return func(id protocol.ID, key ic.PrivKey, muxers []tptu.StreamMuxer) (*Transport, error) {
		localPeer, err := peer.IDFromPrivateKey(key)
		if err != nil {
			return nil, err
		}
		identity, err := libp2ptls.NewIdentity(key, libp2ptls.WithCertTemplate(certTemplate(creds.Cert)))
		if err != nil {
			return nil, err
		}
		muxerIDs := make([]protocol.ID, 0, len(muxers))
		for _, m := range muxers {
			muxerIDs = append(muxerIDs, m.ID)
		}
		return &Transport{
			identity:   identity,
			creds:      creds,
			localPeer:  localPeer,
			muxers:     muxerIDs,
			protocolID: id,
		}, nil
	}
}

func (t *Transport) ID() protocol.ID { return t.protocolID }

func (t *Transport) configForPeer(remote peer.ID) (*tls.Config, <-chan ic.PubKey) {
	conf, _ := t.identity.ConfigForPeer(remote)
	keyCh := make(chan ic.PubKey, 1)
	conf.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		chain := make([]*x509.Certificate, len(rawCerts))
		for i := range rawCerts {
			c, err := x509.ParseCertificate(rawCerts[i])
			if err != nil {
				return err
			}
			chain[i] = c
		}
		pub, err := libp2ptls.PubKeyFromCertChain(chain)
		if err != nil {
			return err
		}
		if remote != "" && !remote.MatchesPublicKey(pub) {
			actual, err := peer.IDFromPublicKey(pub)
			if err != nil {
				return err
			}
			return sec.ErrPeerIDMismatch{Expected: remote, Actual: actual}
		}
		if err := t.creds.Verify(extract(chain[0]), pub); err != nil {
			return err
		}
		keyCh <- pub
		return nil
	}
	return conf, keyCh
}

func (t *Transport) SecureInbound(ctx context.Context, insecure net.Conn, p peer.ID) (sec.SecureConn, error) {
	config, keyCh := t.configForPeer(p)
	muxers := make([]string, 0, len(t.muxers))
	for _, m := range t.muxers {
		muxers = append(muxers, string(m))
	}
	getConfigForClient := config.GetConfigForClient
	config.GetConfigForClient = func(info *tls.ClientHelloInfo) (*tls.Config, error) {
	alpnLoop:
		for _, proto := range info.SupportedProtos {
			for _, m := range muxers {
				if m == proto {
					config.NextProtos = []string{proto}
					break alpnLoop
				}
			}
		}
		if getConfigForClient != nil {
			return getConfigForClient(info)
		}
		return config, nil
	}
	config.NextProtos = append(muxers, config.NextProtos...)
	cs, err := t.handshake(ctx, tls.Server(insecure, config), keyCh)
	if err != nil {
		insecure.Close()
	}
	return cs, err
}

// SecureOutbound: per TLS 1.3, a server refusing our certificate surfaces on
// the first Read, not here.
func (t *Transport) SecureOutbound(ctx context.Context, insecure net.Conn, p peer.ID) (sec.SecureConn, error) {
	config, keyCh := t.configForPeer(p)
	muxers := make([]string, 0, len(t.muxers))
	for _, m := range t.muxers {
		muxers = append(muxers, string(m))
	}
	config.NextProtos = append(muxers, config.NextProtos...)
	cs, err := t.handshake(ctx, tls.Client(insecure, config), keyCh)
	if err != nil {
		insecure.Close()
	}
	return cs, err
}

func (t *Transport) handshake(ctx context.Context, tlsConn *tls.Conn, keyCh <-chan ic.PubKey) (_sconn sec.SecureConn, err error) {
	defer func() {
		if rerr := recover(); rerr != nil {
			fmt.Fprintf(os.Stderr, "panic in sectls handshake: %s\n%s\n", rerr, debug.Stack())
			err = fmt.Errorf("panic in sectls handshake: %s", rerr)
		}
	}()
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	var remotePubKey ic.PubKey
	select {
	case remotePubKey = <-keyCh:
	default:
	}
	if remotePubKey == nil {
		return nil, errors.New("sectls: expected remote pub key to be set after handshake")
	}
	return t.setupConn(tlsConn, remotePubKey)
}

func (t *Transport) setupConn(tlsConn *tls.Conn, remotePubKey ic.PubKey) (sec.SecureConn, error) {
	remotePeerID, err := peer.IDFromPublicKey(remotePubKey)
	if err != nil {
		return nil, err
	}
	nextProto := tlsConn.ConnectionState().NegotiatedProtocol
	if nextProto == "libp2p" {
		nextProto = ""
	}
	return &conn{
		Conn:         tlsConn,
		localPeer:    t.localPeer,
		remotePeer:   remotePeerID,
		remotePubKey: remotePubKey,
		connectionState: network.ConnectionState{
			StreamMultiplexer:         protocol.ID(nextProto),
			UsedEarlyMuxerNegotiation: nextProto != "",
		},
	}, nil
}

func certTemplate(admission []byte) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:    serial(),
		NotBefore:       time.Now().Add(-time.Hour),
		NotAfter:        time.Now().Add(certValidityPeriod),
		Subject:         pkix.Name{SerialNumber: serial().String()},
		ExtraExtensions: []pkix.Extension{{Id: oidAdmission, Value: admission}},
	}
}
