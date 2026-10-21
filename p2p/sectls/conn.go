package sectls

import (
	"crypto/tls"

	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/sec"
)

// conn is a sec.SecureConn over the TLS connection, as go-libp2p's own.
type conn struct {
	*tls.Conn

	localPeer       peer.ID
	remotePeer      peer.ID
	remotePubKey    ic.PubKey
	connectionState network.ConnectionState
}

var _ sec.SecureConn = &conn{}

func (c *conn) LocalPeer() peer.ID                 { return c.localPeer }
func (c *conn) RemotePeer() peer.ID                { return c.remotePeer }
func (c *conn) RemotePublicKey() ic.PubKey         { return c.remotePubKey }
func (c *conn) ConnState() network.ConnectionState { return c.connectionState }
