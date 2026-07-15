package p2p

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/services/substrate/components/p2p"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getCommand(clientId uint32) (*Command, errno.Error) {
	f.commandsLock.RLock()
	defer f.commandsLock.RUnlock()
	if cmd, ok := f.commands[clientId]; ok {
		return cmd, 0
	}

	return nil, errno.ErrorClientNotFound
}

func (f *Factory) getOrCreateStream(protocol string) (p2p.Stream, errno.Error) {
	ctx := f.parent.Context()

	f.streamsLock.RLock()
	defer f.streamsLock.RUnlock()

	stream, ok := f.streams[ctx.Project()+ctx.Application()+protocol]
	if !ok {
		var err error
		stream, err = f.p2pNode.Stream(ctx.Context(), ctx.Project(), ctx.Application(), protocol)
		if err != nil {
			return nil, errno.ErrorP2PProtocolNotFound
		}

		f.streams[ctx.Project()+ctx.Application()+protocol] = stream
	}

	return stream, 0
}

func (f *Factory) newCommand(
	ctx context.Context,
	module common.Module,
	protocolPtr, protocolLen,
	commandPtr, commandLen,
	commandIdPtr uint32,
) (err uint32) {
	protocol, err0 := f.ReadString(module, protocolPtr, protocolLen)
	if err0 != 0 {
		return uint32(err0)
	}

	command, err0 := f.ReadString(module, commandPtr, commandLen)
	if err0 != 0 {
		return uint32(err0)
	}

	stream, err0 := f.getOrCreateStream(protocol)
	if err0 != 0 {
		return uint32(err0)
	}

	cmd, err0err := stream.Command(command)
	if err0err != nil {
		return uint32(errno.ErrorCommandCreateFailed)
	}

	_cmd := &Command{
		Command: cmd,
		Id:      f.generateCommandId(),
	}

	f.commandsLock.Lock()
	defer f.commandsLock.Unlock()
	f.commands[_cmd.Id] = _cmd

	return uint32(f.WriteUint32Le(module, commandIdPtr, _cmd.Id))
}

func (f *Factory) listenToProtocolSize(ctx context.Context, module common.Module,
	protocolPtr, protocolLen,
	responseSizePtr uint32,
) (err uint32) {
	protocol, err0 := f.ReadString(module, protocolPtr, protocolLen)
	if err0 != 0 {
		return uint32(err0)
	}

	stream, err0 := f.getOrCreateStream(protocol)
	if err0 != 0 {
		return uint32(err0)
	}

	protocolToSend, err0err := stream.Listen()
	if err0err != nil {
		return uint32(errno.ErrorP2PListenFailed)
	}

	f.setListenProtocol(protocolToSend)

	return uint32(f.WriteStringSize(module, responseSizePtr, protocolToSend))
}

func (f *Factory) listenToProtocol(ctx context.Context, module common.Module,
	protocolPtr, protocolLen,
	response, responseSize uint32,
) (err uint32) {
	protocol := f.getListenProtocol()

	if responseSize != uint32(len(protocol)) {
		return uint32(errno.ErrorEOF)
	}

	return uint32(f.WriteString(module, response, protocol))
}

func (f *Factory) setListenProtocol(protocol string) {
	f.listenProtocol = protocol
}

func (f *Factory) getListenProtocol() string {
	return f.listenProtocol
}
