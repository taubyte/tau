package client

import (
	"context"
	"sync"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/tau/core/services/substrate/components/ipfs"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	ipfsNode        ipfs.Service
	parent          vm.Instance
	ctx             context.Context
	clients         map[uint32]*Client
	clientsLock     sync.RWMutex
	clientsIdToGrab uint32
}

var _ vm.Factory = &Factory{}

type Client struct {
	Id              uint32
	contentIdToGrab uint32
	contentLock     sync.RWMutex
	Contents        map[uint32]*content
}

type content struct {
	id   uint32
	cid  cid.Cid
	file file
}

type file interface{}
