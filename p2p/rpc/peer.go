package rpc

import (
	"context"
	"errors"
	"net"

	"github.com/libp2p/go-libp2p/core/peer"
)

// ErrNoRemotePeer is returned by RemotePeer when the request did not arrive
// over a libp2p stream this package accepted. It is the safe answer: a
// handler that cannot name its caller must not guess one.
var ErrNoRemotePeer = errors.New("request did not arrive over a libp2p stream")

type remotePeerKey struct{}

// carryRemotePeer is http.Server.ConnContext: it puts the accepted stream's
// peer into the connection context, from which every request on that
// connection inherits it.
func carryRemotePeer(ctx context.Context, c net.Conn) context.Context {
	if sc, ok := c.(interface{ RemotePeerID() peer.ID }); ok {
		if id := sc.RemotePeerID(); id != "" {
			return context.WithValue(ctx, remotePeerKey{}, id)
		}
	}
	return ctx
}

// RemotePeer returns the libp2p peer that opened the stream this request
// arrived on. Pass a connect handler's ctx, or an http.Handler's
// r.Context().
//
// The value is the identity libp2p established when it secured the
// connection, taken from the stream at accept time. Nothing the caller writes
// — no header, no body, no authority — can reach it, and a request that came
// in over anything but one of this package's listeners returns
// ErrNoRemotePeer rather than an identity. This is what v2 services
// authenticate on.
//
// connect's own req.Peer().Addr also holds the peer id, because that is what
// gostream reports as the conn's remote address. It is the same fact arriving
// as a string that has to be parsed and that a plain TCP listener would fill
// with something else entirely; prefer this.
func RemotePeer(ctx context.Context) (peer.ID, error) {
	id, ok := ctx.Value(remotePeerKey{}).(peer.ID)
	if !ok || id == "" {
		return "", ErrNoRemotePeer
	}
	return id, nil
}
