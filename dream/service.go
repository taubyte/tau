package dream

import (
	"fmt"
	"os"
	"path"
	"sort"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
)

func (u *Universe) createService(name string, config *commonIface.ServiceConfig) error {
	if config.Disabled {
		return nil
	}

	if config.Root == "" {
		config.Root = u.root
	}

	// Create the slot on demand, for the same reason handlerRegistry.Set does:
	// the map is pre-seeded from commonSpecs.Services, and a build-tag-gated
	// service registered from an init() is not in that list. Without this the
	// lookup is a nil map entry and the deref below panics.
	serviceCount := u.reserveSlot(name)
	config.Root = path.Join(config.Root, fmt.Sprintf("%s-%d", name, serviceCount))
	// Ignoring error in case of opening
	os.MkdirAll(config.Root, 0750)

	if config.Others == nil {
		config.Others = make(map[string]int)
	}

	// WHICH COPY THIS IS, so a node can be told apart from its siblings by
	// something other than a key it mints at boot. `copies: 4` is four nodes
	// from one config, and without an ordinal the only thing that differs
	// between them is a random peer id — which is not something a spread can
	// be built on. See node/dream.regionIn, which is the reason this exists.
	config.Others[CopyKey] = serviceCount

	// NO PORTS ARE CHOSEN HERE. The service listens on 0 and the kernel
	// allocates at bind time (dream/common.NewConfig), so there is nothing to
	// reserve and nothing to collide over. The retry-on-"address already in
	// use" loop this replaced existed only to paper over the window between
	// reserving a port and binding it; with no window there is nothing to
	// retry, and a bind that fails now says something real.
	service, err := u.startService(name, config)
	if err != nil {
		return err
	}
	node := service.Node()

	// THE PORTS ARE READ BACK, NEVER PREDICTED, so what a universe reports —
	// and what u.GetPort hands a test — is the port that was actually taken.
	p2pPort, err := boundP2PPort(node)
	if err != nil {
		return fmt.Errorf("service `%s`: %w", name, err)
	}
	config.Others["p2p"] = p2pPort
	config.Port = p2pPort
	if httpPort, serving := boundHTTPPort(service); serving {
		config.Others["http"] = httpPort
	}

	config.Databases = kvdb.New(node)

	// we mesh first
	u.Mesh(node)

	// Wait (bounded, best-effort) until at least one peer has connected.
	// Skipping this — even for the first-booted node, which pays the full
	// timeout — lets a service make its first pubsub/DHT moves while
	// isolated; those failures get backoff-cached for 60s+ and wedge the
	// universe for minutes.
	node.WaitForSwarm(time.Second)

	// register so others can mesh with it
	u.Register(node, name, config.Others)

	return nil
}

func (u *Universe) Service(name string, config *commonIface.ServiceConfig) error {
	//make sure we have swarm key
	config.SwarmKey = u.swarmKey

	if config.Others == nil {
		config.Others = make(map[string]int)
	}

	if config.Others["copies"] <= 0 {
		config.Others["copies"] = 1
	}

	for i := 0; i < config.Others["copies"]; i++ {
		err := u.createService(name, config.Clone())
		if err != nil {
			return err
		}
	}

	return nil
}

// reserveSlot makes this service's entry if it has none and answers how many
// nodes it already has.
//
// IT TAKES THE LOCK, which createService did not and registerService always
// has — the same map, guarded in one place and not the other. That was
// survivable while every service in a universe came pre-seeded from
// commonSpecs.Services, because then nothing ever WROTE here. A v2 service
// registers from an init() and is not in that list, so it takes the create
// branch — and StartWithConfig starts every service in its own goroutine, so
// two of them writing this map at once is a concurrent map write and the
// process dies.
//
// THE LOCK IS RELEASED BEFORE THE SERVICE IS STARTED, which is not tidiness: a
// service calls registerService as it comes up, and that takes this same lock.
// Holding it across the start would deadlock on the first node.
// CopyKey is where a node reads which copy of its config it is, counting from
// zero. It is not a service and not a region: both of those are prefixed, and
// every reader of Others matches on its own prefix.
const CopyKey = "copy"

func (u *Universe) reserveSlot(name string) int {
	u.lock.Lock()
	defer u.lock.Unlock()

	si, ok := u.service[name]
	if !ok {
		si = &serviceInfo{nodes: make(map[string]commonIface.Service)}
		u.service[name] = si
	}
	return len(si.nodes)
}

func (u *Universe) registerService(name string, srv commonIface.Service) peer.Node {
	u.lock.Lock()
	defer u.lock.Unlock()
	registered, ok := u.service[name]
	if !ok {
		registered = &serviceInfo{
			nodes: make(map[string]commonIface.Service),
		}
		u.service[name] = registered
	}
	registered.nodes[srv.Node().ID().String()] = srv
	return srv.Node()
}

func (u *Universe) GetServicePids(name string) ([]string, error) {
	u.lock.RLock()
	defer u.lock.RUnlock()

	nodes, ok := u.service[name]
	if !ok {
		return nil, fmt.Errorf("%s was not found", name)
	}

	pids := make([]string, 0)
	for pid := range nodes.nodes {
		pids = append(pids, pid)
	}

	return pids, nil
}

// Services is every protocol that actually has a service to start, sorted.
//
// IT IS NOT commonSpecs.Services, and that is the point. The registry is
// pre-seeded from that list so a slot exists to Set into, so a name being IN it
// says only that somebody once expected a service by that name — not that one
// registers today. `seer` and `monkey` are in it and were deleted, so anything
// walking the list asked for a service with a nil creator and got an error
// naming a universe rather than a missing service.
//
// A LIST OF WHAT EXISTS, MAINTAINED BY HAND, drifts the moment something is
// removed. This asks what registered.
func (h *handlerRegistry) Services() []string {
	h.lock.RLock()
	defer h.lock.RUnlock()

	out := make([]string, 0, len(h.registry))
	for protocol, hs := range h.registry {
		if hs != nil && hs.service != nil {
			out = append(out, protocol)
		}
	}
	sort.Strings(out)
	return out
}

func (h *handlerRegistry) Set(protocol string, service ServiceCreate, client ClientCreate) error {
	if protocol == "" {
		return fmt.Errorf("protocol required")
	}

	h.lock.Lock()
	defer h.lock.Unlock()

	// Create the slot on demand: services pre-seeded from commonSpecs.Services
	// already have one, but a build-tag-gated (e.g. ee) service registered from
	// an init() may not be in that list yet, so Set is what admits it.
	hs, ok := h.registry[protocol]
	if !ok {
		hs = &handlers{}
		h.registry[protocol] = hs
	}

	if service != nil {
		hs.service = service
	}

	if client != nil {
		hs.client = client
	}

	return nil
}

func (h *handlerRegistry) client(protocol string) (ClientCreate, error) {
	handlers, err := h.handlers(protocol)
	if err != nil {
		return nil, err
	}

	if handlers.client == nil {
		return nil, fmt.Errorf("client creation method is nil have you imported _ \"github.com/taubyte/tau/clients/p2p/%s/dream\"", protocol)
	}

	return handlers.client, nil
}

func (h *handlerRegistry) service(protocol string) (ServiceCreate, error) {
	handlers, err := h.handlers(protocol)
	if err != nil {
		return nil, err
	}

	if handlers.service == nil {
		return nil, fmt.Errorf("Service creation method is nil have you imported _ \"github.com/taubyte/tau/services/%s/dream\"", protocol)
	}

	return handlers.service, nil
}

func (h *handlerRegistry) handlers(protocol string) (*handlers, error) {
	h.lock.RLock()
	handlers, ok := h.registry[protocol]
	h.lock.RUnlock()
	if !ok {
		return nil, fmt.Errorf("protocol `%s` does not exist", protocol)
	}

	return handlers, nil
}
