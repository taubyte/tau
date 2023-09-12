package libdream

import (
	"context"
	"fmt"
	"sync"

	peercore "github.com/libp2p/go-libp2p/core/peer"
	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/auth"
	"github.com/taubyte/go-interfaces/services/gateway"
	"github.com/taubyte/go-interfaces/services/hoarder"
	"github.com/taubyte/go-interfaces/services/monkey"
	"github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/go-interfaces/services/seer"
	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/services/tns"
	commonSpecs "github.com/taubyte/go-specs/common"
	peer "github.com/taubyte/p2p/peer"
)

func (u *Universe) Name() string {
	return u.name
}

func (u *Universe) All() []peer.Node {
	return u.all
}

func (u *Universe) Lookup(id string) (*NodeInfo, bool) {
	u.lock.RLock()
	node, exist := u.lookups[id]
	u.lock.RUnlock()
	if !exist {
		return nil, false
	}
	return node, true
}

func (u *Universe) Root() string {
	return u.root
}

func (u *Universe) Context() context.Context {
	return u.ctx
}

func (u *Universe) Mesh(newNodes ...peer.Node) {
	ctx, ctxC := context.WithTimeout(u.ctx, MeshTimeout)
	defer ctxC()

	u.lock.RLock()
	var wg sync.WaitGroup
	for _, n0 := range newNodes {
		for _, n1 := range u.all {
			if n0 != n1 {
				wg.Add(1)
				go func(n0, n1 peer.Node) {
					defer wg.Done()
					n0.Peer().Connect(
						ctx,
						peercore.AddrInfo{
							ID:    n1.ID(),
							Addrs: n1.Peer().Addrs(),
						},
					)
				}(n0, n1)
			}
		}
	}
	wg.Wait()
	u.lock.RUnlock()

	u.lock.Lock()
	u.all = append(u.all, newNodes...)
	u.lock.Unlock()
}

func (u *Universe) Seer() seer.Service {
	ret, ok := first[seer.Service](u, u.service["seer"].nodes)
	if !ok {
		return nil
	}
	return ret
}

func (u *Universe) SeerByPid(pid string) (seer.Service, bool) {
	return byId[seer.Service](u, u.service["seer"].nodes, pid)
}

func (u *Universe) Auth() auth.Service {
	ret, ok := first[auth.Service](u, u.service["auth"].nodes)
	if !ok {
		return nil
	}
	return ret
}

func (u *Universe) AuthByPid(pid string) (auth.Service, bool) {
	return byId[auth.Service](u, u.service["auth"].nodes, pid)
}

func (u *Universe) Patrick() patrick.Service {
	ret, ok := first[patrick.Service](u, u.service["patrick"].nodes)
	if !ok {
		return nil
	}
	return ret
}

func (u *Universe) PatrickByPid(pid string) (patrick.Service, bool) {
	return byId[patrick.Service](u, u.service["patrick"].nodes, pid)
}

func (u *Universe) TNS() tns.Service {
	ret, ok := first[tns.Service](u, u.service["tns"].nodes)
	if !ok {
		return nil
	}
	return ret
}

func (u *Universe) TnsByPid(pid string) (tns.Service, bool) {
	return byId[tns.Service](u, u.service["tns"].nodes, pid)
}

func (u *Universe) Monkey() monkey.Service {
	ret, ok := first[monkey.Service](u, u.service["monkey"].nodes)
	if !ok {
		return nil
	}
	return ret
}

func (u *Universe) Gateway() gateway.Service {
	ret, ok := first[gateway.Service](u, u.service["gateway"].nodes)
	if !ok {
		return nil
	}
	return ret
}

func (u *Universe) MonkeyByPid(pid string) (monkey.Service, bool) {
	return byId[monkey.Service](u, u.service["monkey"].nodes, pid)
}

func (u *Universe) Hoarder() hoarder.Service {
	ret, ok := first[hoarder.Service](u, u.service["hoarder"].nodes)
	if !ok {
		return nil
	}
	return ret
}

func (u *Universe) HoarderByPid(pid string) (hoarder.Service, bool) {
	return byId[hoarder.Service](u, u.service["hoarder"].nodes, pid)
}

func (u *Universe) Substrate() substrate.Service {
	ret, ok := first[substrate.Service](u, u.service["substrate"].nodes)
	if !ok {
		return nil
	}
	return ret
}

func (u *Universe) SubstrateByPid(pid string) (substrate.Service, bool) {
	return byId[substrate.Service](u, u.service["substrate"].nodes, pid)
}

func byId[T any](u *Universe, i map[string]commonIface.Service, pid string) (T, bool) {
	var result T
	u.lock.RLock()
	defer u.lock.RUnlock()
	a, ok := i[pid]
	if !ok {
		return result, false
	}
	_a, ok := a.(T)
	return _a, ok
}

func first[T any](u *Universe, i map[string]commonIface.Service) (T, bool) {
	var _nil T
	u.lock.RLock()
	defer u.lock.RUnlock()
	for _, s := range i {
		_s, ok := s.(T)
		if !ok || s == nil {
			return _nil, false
		}
		return _s, true
	}
	return _nil, false
}

func (u *Universe) ListNumber(name string) int {
	return len(u.service[name].nodes)
}

func (u *Universe) Kill(name string) error {
	var isService bool
	for _, service := range commonSpecs.Protocols {
		if name == service {
			isService = true
			break
		}
	}

	if isService {
		ids, err := u.GetServicePids(name)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return fmt.Errorf("killing %s failed with: does not exist", name)
		}

		return u.killServiceByNameId(name, ids[0])

	} else {
		u.lock.RLock()
		simple, exist := u.simples[name]
		u.lock.RUnlock()
		if !exist {
			return fmt.Errorf("killing %s failed with: does not exist", name)
		}

		return u.killSimpleByNameId(name, simple.ID().Pretty())
	}
}
