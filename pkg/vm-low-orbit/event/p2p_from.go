package event

import (
	"context"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_getP2PEventFrom(ctx context.Context, module common.Module, eventId, cidPtr uint32) errno.Error {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return err
	}

	conn, err0 := data.cmd.Connection()
	if err0 != nil {
		return err
	}

	_from := conn.RemotePeer()
	if len(_from) == 0 {
		return errno.ErrorP2PFromNotFound
	}

	return f.WriteCid(module, cidPtr, peer.ToCid(_from))
}
