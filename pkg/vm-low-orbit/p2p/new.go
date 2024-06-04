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

func (f *Factory) W_newCommand(
	ctx context.Context,
	module common.Module,
	protocolPtr, protocolLen,
	commandPtr, commandLen,
	commandIdPtr uint32,
) (err errno.Error) {
	protocol, err := f.ReadString(module, protocolPtr, protocolLen)
	if err != 0 {
		return
	}

	command, err := f.ReadString(module, commandPtr, commandLen)
	if err != 0 {
		return
	}

	stream, err := f.getOrCreateStream(protocol)
	if err != 0 {
		return
	}

	cmd, err0 := stream.Command(command)
	if err0 != nil {
		return errno.ErrorCommandCreateFailed
	}

	_cmd := &Command{
		Command: cmd,
		Id:      f.generateCommandId(),
	}

	f.commandsLock.Lock()
	defer f.commandsLock.Unlock()
	f.commands[_cmd.Id] = _cmd

	return f.WriteUint32Le(module, commandIdPtr, _cmd.Id)
}

func (f *Factory) W_listenToProtocolSize(ctx context.Context, module common.Module,
	protocolPtr, protocolLen,
	responseSizePtr uint32,
) (err errno.Error) {
	protocol, err := f.ReadString(module, protocolPtr, protocolLen)
	if err != 0 {
		return
	}

	stream, err := f.getOrCreateStream(protocol)
	if err != 0 {
		return
	}

	protocolToSend, err0 := stream.Listen()
	if err0 != nil {
		return errno.ErrorP2PListenFailed
	}

	f.setListenProtocol(protocolToSend)

	return f.WriteStringSize(module, responseSizePtr, protocolToSend)
}

func (f *Factory) W_listenToProtocol(ctx context.Context, module common.Module,
	protocolPtr, protocolLen,
	response, responseSize uint32,
) (err errno.Error) {
	protocol := f.getListenProtocol()

	if responseSize != uint32(len(protocol)) {
		return errno.ErrorEOF
	}

	return f.WriteString(module, response, protocol)
}

func (f *Factory) setListenProtocol(protocol string) {
	f.listenProtocol = protocol
}

func (f *Factory) getListenProtocol() string {
	return f.listenProtocol
}
