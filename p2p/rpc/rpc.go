// Package rpc carries ConnectRPC between tau nodes over libp2p streams.
//
// go-libp2p-gostream turns a libp2p stream into a net.Conn, which is all
// net/http needs: the server runs on a gostream listener, the client dials
// through gostream. Everything above the conn is stock — connect-go generates
// the handlers and the clients, buf generates connect-go, and this package
// only supplies the pipe. pkg/raft/transport.go carries hashicorp/raft the
// same way.
//
// The package is named rpc rather than connect because every service that
// uses it also imports connectrpc.com/connect, whose package name is connect.
// Sharing that name would cost each of them an alias for one of the two.
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
// # What this package bounds, and what it does not
//
// It bounds the conn: a freshly accepted stream has DefaultPrefaceTimeout to
// deliver the whole HTTP/2 client preface, and everything net/http and http2
// bound themselves — concurrent streams, header size, flow-control windows,
// idle time — is reachable through WithHTTPServer.
//
// IT DOES NOT BOUND A MESSAGE, and that is deliberate rather than missing.
// connect's ceiling is connect.WithReadMaxBytes, a handler and client option:
//
//	mux.Handle(seerv2connect.NewSeerServiceHandler(svc, connect.WithReadMaxBytes(4<<20)))
//
// It cannot be set from here. This package never constructs a connect handler
// or client — it takes an http.Handler and hands back an *http.Client — and
// the number is per method anyway, since a config push and a heartbeat do not
// share one. Capping the request body instead would be the wrong layer twice
// over: a client-streaming body is many messages and a bidi body has no
// length at all, so a body cap would refuse the calls streaming exists for,
// and it would refuse them as a transport error rather than as connect's
// CodeResourceExhausted. What the pipe does guarantee is that a peer cannot
// outrun a handler that is not reading: http2 flow control holds unread bytes
// to its per-stream and per-connection windows. Deciding how much a handler
// will hold in memory once it does read is the service's call, and every v2
// service is expected to make it.
package rpc

import "net/http"

// h2 is the protocol set both ends of the pipe agree on: HTTP/2 over a
// cleartext conn, no HTTP/1.
func h2() *http.Protocols {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	return p
}
