package client

import (
	"context"
	"net/http"
	"sync"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	parent          vm.Instance
	ctx             context.Context
	clientsLock     sync.RWMutex
	clientsIdToGrab uint32
	clients         map[uint32]*Client
}

var _ vm.Factory = &Factory{}

type Client struct {
	*http.Client
	Id          uint32
	reqLock     sync.RWMutex
	reqIdToGrab uint32
	reqs        map[uint32]*Request
}

type Request struct {
	*http.Request
	Id uint32
}
