package service

import (
	"fmt"

	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command/router"
)

type CommandService interface {
	Define(command string, handler router.CommandHandler) error
	DefineStream(command string, std router.CommandHandler, stream router.StreamHandler) error
	Start()
	Stop()
	Router() *(router.Router)
}
type commandService struct {
	//ctx    context.Context
	name   string
	peer   peer.Node
	router *(router.Router)
	stream *(streams.StreamManger)
}

func New(peer peer.Node, name string, path string) (*commandService, error) {
	var cs commandService

	cs.name = name
	cs.peer = peer

	cs.stream = streams.New(peer, name, path)
	if cs.stream == nil {
		return nil, fmt.Errorf("creating stream service for %q on path %q failed", name, path)
	}

	cs.router = router.New(cs.stream)
	if cs.router == nil {
		return nil, fmt.Errorf("creating command router for service %q failed", name)
	}

	// Don't start here - caller should register handlers first, then call Start()
	return &cs, nil
}

// Start begins accepting connections. Call after all handlers are registered.
func (cs *commandService) Start() {
	cs.stream.Start(func(s streams.Stream) { cs.router.Handle(s) })
}

func (cs *commandService) Stop() {
	cs.stream.Stop()
}

func (cs *commandService) Router() *(router.Router) {
	return cs.router
}

func (cs *commandService) Define(command string, handler router.CommandHandler) error {
	if err := cs.router.AddStatic(command, handler, nil); err != nil {
		return fmt.Errorf("defining command %q failed: %w", command, err)
	}
	return nil
}

func (cs *commandService) DefineStream(command string, std router.CommandHandler, stream router.StreamHandler) error {
	if err := cs.router.AddStatic(command, std, stream); err != nil {
		return fmt.Errorf("defining stream command %q failed: %w", command, err)
	}
	return nil
}
