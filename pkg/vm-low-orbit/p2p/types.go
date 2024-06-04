package p2p

import (
	"context"
	"sync"

	"github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	p2pNode          p2p.Service
	parent           vm.Instance
	ctx              context.Context
	commandsLock     sync.RWMutex
	commandsIdToGrab uint32
	commands         map[uint32]*Command

	streamsLock sync.RWMutex
	streams     map[string]p2p.Stream

	discoverLock     sync.RWMutex
	discoverIdToGrab uint32
	discover         map[uint32][][]byte

	listenProtocol string
}

var _ vm.Factory = &Factory{}

type Command struct {
	p2p.Command
	Id   uint32
	Body []byte
}
