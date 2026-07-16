//go:build web3
// +build web3

package ethereum

import (
	"context"
	"math/big"
	"reflect"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/core/types"
	pubsubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

// Backend is the slice of an eth node the plugin actually uses: the contract
// read/write/filter surface (bind.ContractBackend, needed by DeployContract and
// bound contracts) plus the block/chain reads. *ethclient.Client satisfies it;
// so does an in-memory node, which is how tests avoid a live RPC endpoint.
// Close is deliberately absent — ethclient's Close() and the simulated backend's
// Close() error have different signatures — and is handled per-client instead.
type Backend interface {
	bind.ContractBackend
	BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error)
	BlockNumber(ctx context.Context) (uint64, error)
	ChainID(ctx context.Context) (*big.Int, error)
}

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
	Backend
	closeFn           func()
	Id                uint32
	blocks            map[uint64]*Block
	blocksLock        sync.RWMutex
	contracts         map[uint32]*Contract
	contractsLock     sync.RWMutex
	contractsIdToGrab uint32
}

// Close releases the underlying backend (real RPC connection, or a no-op for an
// injected in-memory backend).
func (c *Client) Close() {
	if c.closeFn != nil {
		c.closeFn()
	}
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
