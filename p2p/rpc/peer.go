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

// RemotePeer returns the libp2p peer that opened the stream this request arrived
// on. Pass a connect handler's ctx, or an http.Handler's r.Context().
//
// The identity is the one libp2p established when securing the connection, taken at
// accept time; nothing the caller writes can reach it. A request that arrived any
// other way is ErrNoRemotePeer. v2 services authenticate on this, not on connect's
// req.Peer().Addr string.
func RemotePeer(ctx context.Context) (peer.ID, error) {
	id, ok := ctx.Value(remotePeerKey{}).(peer.ID)
	if !ok || id == "" {
		return "", ErrNoRemotePeer
	}
	return id, nil
}
