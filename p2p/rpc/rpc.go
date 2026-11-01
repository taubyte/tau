// Package rpc carries ConnectRPC between tau nodes over libp2p streams.
//
// go-libp2p-gostream turns a libp2p stream into a net.Conn, which is all net/http
// needs; everything above the conn is stock connect-go. It is named rpc so services
// importing connectrpc.com/connect need no alias.
//
// The pipe speaks unencrypted HTTP/2 with prior knowledge and no HTTP/1 fallback:
// connect's streaming shapes need HTTP/2, and libp2p already encrypted and
// authenticated the stream. A caller's identity comes from the stream (RemotePeer).
//
// It bounds the conn: an accepted stream has DefaultPrefaceTimeout to deliver the
// HTTP/2 client preface, and everything net/http and http2 bound is reachable through
// WithHTTPServer.
//
// It does not bound a message. That is connect.WithReadMaxBytes, per handler and
// method:
//
//	mux.Handle(seerv2connect.NewSeerServiceHandler(svc, connect.WithReadMaxBytes(4<<20)))
//
// A body cap here would refuse streaming calls as a transport error. http2 flow
// control already stops a peer outrunning a handler that is not reading; how much a
// handler holds once it reads is each service's call.
package rpc

import "net/http"

// h2 is the protocol set both ends of the pipe agree on: HTTP/2 over a
// cleartext conn, no HTTP/1.
func h2() *http.Protocols {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	return p
}
