// Package connect carries ConnectRPC between tau nodes over libp2p streams.
//
// go-libp2p-gostream turns a libp2p stream into a net.Conn, which is all
// net/http needs: the server runs on a gostream listener, the client dials
// through gostream. Everything above the conn is stock — connect-go generates
// the handlers and the clients, buf generates connect-go, and this package
// only supplies the pipe. pkg/raft/transport.go carries hashicorp/raft the
// same way.
//
// The pipe speaks unencrypted HTTP/2 with prior knowledge, on both ends and
// with no HTTP/1 fallback. HTTP/2 is not a preference here: connect's three
// streaming shapes need one, and a caller that silently fell back to HTTP/1.1
// would get a unary-only connection whose "streams" arrive all at once.
// Unencrypted is correct too — libp2p has already encrypted and authenticated
// the stream, and a second handshake inside it would buy nothing.
//
// A caller's identity comes from the stream, never from what the caller sent:
// see RemotePeer.
//
// Callers that also import connectrpc.com/connect have to alias one of the
// two package names.
package connect

import "net/http"

// h2 is the protocol set both ends of the pipe agree on: HTTP/2 over a
// cleartext conn, no HTTP/1.
func h2() *http.Protocols {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	return p
}
