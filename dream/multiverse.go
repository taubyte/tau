package dream

import (
	"context"
	"fmt"

	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

var multiverse Multiverse

func MultiVerse() *Multiverse {
	return &multiverse
}

// kill them all
// ref: https://dragonball.fandom.com/wiki/Zeno
func Zeno() {
	universesLock.Lock()
	defer universesLock.Unlock()
	for _, u := range universes {
		u.Cleanup()
	}
	multiverseCtxC()
}

func (m *Multiverse) Context() context.Context {
	return multiverseCtx
}

func (m *Multiverse) Universe(name string) (*Universe, error) {
	universesLock.RLock()
	defer universesLock.RUnlock()
	u, exists := universes[name]
	if !exists {
		return nil, fmt.Errorf("universe not found")
	}
	return u, nil
}

func (m *Multiverse) Delete(name string) error {
	universesLock.RLock()
	defer universesLock.RUnlock()

	if _, exists := universes[name]; !exists {
		return fmt.Errorf("universe not found")
	}

	delete(universes, name)

	return nil
}

func (m *Multiverse) Status() Status {
	status := make(Status)
	universesLock.RLock()
	defer universesLock.RUnlock()
	for _, u := range universes {
		u.lock.RLock()
		status[u.name] = UniverseStatus{
			Root:      u.root,
			SwarmKey:  u.swarmKey,
			NodeCount: len(u.all),
			Simples: func() []string {
				_simples := make([]string, 0)
				for s := range u.simples {
					_simples = append(_simples, s)
				}
				return _simples
			}(),
			Nodes: func() map[string][]string {
				_nodes := make(map[string][]string)
				u.lock.RLock()
				defer u.lock.RUnlock()
				for _, s := range u.all {
					paddrs := s.Peer().Addrs()
					addrs := make([]string, 0, len(paddrs))
					for _, addr := range paddrs {
						addrs = append(addrs, addr.String())
					}
					_nodes[s.ID().String()] = addrs
				}
				return _nodes
			}(),
			Services: func() []ServiceStatus {
				_services := make([]ServiceStatus, 0, len(commonSpecs.Services))
				for _, name := range commonSpecs.Services {
					nodes := u.service[name].nodes
					if nodes != nil {
						_services = append(_services, ServiceStatus{Name: name, Copies: len(nodes)})
					}
				}
				return _services
			}(),
		}
		u.lock.RUnlock()
	}
	return status
}

func (m *Multiverse) Universes() Status {
	status := make(Status)
	universesLock.RLock()
	defer universesLock.RUnlock()
	for _, u := range universes {
		u.lock.RLock()
		status[u.name] = UniverseStatus{
			SwarmKey:  u.swarmKey,
			NodeCount: len(u.all),
		}
		u.lock.RUnlock()
	}
	return status
}
