package dns

import (
	"context"
	"net"
	"sync"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	parent            vm.Instance
	ctx               context.Context
	resolversLock     sync.RWMutex
	resolversIdToGrab uint32
	resolvers         map[uint32]*Resolver
}

type Resolver struct {
	*net.Resolver
	responseLock sync.RWMutex
	response     map[ResponseType]map[string][]string
}

var _ vm.Factory = &Factory{}

type ResponseType uint32

const (
	TxTResponse ResponseType = iota
	AddressResponse
	CnameResponse
	MxResponse
)
