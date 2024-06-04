package ethereum

import (
	"context"
	"reflect"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	pubsubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	parent          vm.Instance
	pubsubNode      pubsubIface.Service
	ctx             context.Context
	clients         map[uint32]*Client
	clientsLock     sync.RWMutex
	clientsIdToGrab uint32
}

var _ vm.Factory = &Factory{}

type Client struct {
	*ethclient.Client
	Id                uint32
	blocks            map[uint64]*Block
	blocksLock        sync.RWMutex
	contracts         map[uint32]*Contract
	contractsLock     sync.RWMutex
	contractsIdToGrab uint32
}

type Block struct {
	*types.Block
	transactions     map[uint32]*Transaction
	transactionsLock sync.RWMutex
	Id               uint64
}

type Transaction struct {
	*types.Transaction
	Id uint32
}

type Contract struct {
	*bind.BoundContract
	client             *Client
	Id                 uint32
	abi                *abi.ABI
	methods            map[string]*contractMethod
	events             map[string]*contractEvent
	eventsLock         sync.RWMutex
	transactions       map[uint32]*Transaction
	transactionsLock   sync.RWMutex
	transactionsToGrab uint32
}

type contractMethod struct {
	inputs   []string
	outputs  []string
	constant bool
	data     [][]byte
}

type contractEvent struct {
	parent     *Contract
	event      abi.Event
	structType reflect.Type
	watcher    *contractWatcher
}

type contractWatcher struct {
	lastBlock uint64
	lock      sync.RWMutex
	published map[string]map[uint64]map[uint]struct{}
}
