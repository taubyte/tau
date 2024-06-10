package service

import (
	"errors"

	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command/router"
)

type CommandService struct {
	//ctx    context.Context
	name   string
	peer   peer.Node
	router *(router.Router)
	stream *(streams.StreamManger)
}

func New(peer peer.Node, name string, path string) (*CommandService, error) {
	var cs CommandService

	cs.name = name
	cs.peer = peer

	cs.stream = streams.New(peer, name, path)
	if cs.stream == nil {
		return nil, errors.New("not able to create service")
	}

	cs.router = router.New(cs.stream)
	if cs.router == nil {
		return nil, errors.New("not able to create command router")
	}

	cs.stream.Start(func(s streams.Stream) { cs.router.Handle(s) })
	return &cs, nil
}

func (cs *CommandService) Stop() {
	cs.stream.Stop()
}

func (cs *CommandService) Router() *(router.Router) {
	return cs.router
}

func (cs *CommandService) Define(command string, handler router.CommandHandler) error {
	return cs.router.AddStatic(command, handler, nil)
}

func (cs *CommandService) DefineStream(command string, std router.CommandHandler, stream router.StreamHandler) error {
	return cs.router.AddStatic(command, std, stream)
}
