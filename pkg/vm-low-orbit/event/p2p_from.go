package event

import (
	"context"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getP2PEventFrom(ctx context.Context, module common.Module, eventId, cidPtr uint32) uint32 {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return uint32(err)
	}

	conn, err0 := data.cmd.Connection()
	if err0 != nil {
		return uint32(err)
	}

	_from := conn.RemotePeer()
	if len(_from) == 0 {
		return uint32(errno.ErrorP2PFromNotFound)
	}

	return uint32(f.WriteCid(module, cidPtr, peer.ToCid(_from)))
}
