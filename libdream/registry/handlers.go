package registry

import (
	"context"
	"fmt"
	"sync"

	iface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream/common"
)

type Handlers struct {
	Service func(context.Context, *iface.ServiceConfig) (iface.Service, error)
	Client  func(peer.Node, *iface.ClientConfig) (iface.Client, error)
}

var Registry = struct {
	Auth      Handlers
	Hoarder   Handlers
	Monkey    Handlers
	Patrick   Handlers
	Seer      Handlers
	TNS       Handlers
	Substrate Handlers
	Gateway   Handlers
}{}

// Order of params important!
type FixtureHandler func(universe common.Universe, params ...interface{}) error

var (
	fixtures     map[string]FixtureHandler
	fixturesLock sync.RWMutex
)

func init() {
	fixtures = make(map[string]FixtureHandler)
}

// Register a fixture
func Fixture(name string, handler FixtureHandler) {
	fixturesLock.Lock()
	defer fixturesLock.Unlock()
	fixtures[name] = handler
}

// Returns list of available fixtures
func Fixtures() []string {
	keys := make([]string, 0)
	for key := range fixtures {
		keys = append(keys, key)
	}
	return keys
}

func ValidFixtures() []string {
	keys := make([]string, 0, len(FixtureMap))
	for k := range FixtureMap {
		keys = append(keys, k)
	}

	return keys
}

func Get(fixture string) (FixtureHandler, error) {
	// TODO implement get and set, will probably need a lock
	// on set for importing different fixtures in parallel
	fixturesLock.RLock()
	defer fixturesLock.RUnlock()
	fixtureHandler, exist := fixtures[fixture]
	if !exist {
		importRequired, ok := FixtureMap[fixture]
		if !ok {
			return nil, fmt.Errorf("fixture %s does not exist", fixture)
		}
		return nil, fmt.Errorf("fixture `%s` is nil, have you imported _ \"github.com/taubyte/%s\"", fixture, importRequired.ImportRef)
	}

	return fixtureHandler, nil
}
