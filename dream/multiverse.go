package dream

import (
	"context"
	"fmt"
	"strings"

	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

func New(ctx context.Context) *Multiverse {
	m := &Multiverse{
		universes: make(map[string]*Universe),
	}
	m.ctx, m.ctxC = context.WithCancel(ctx)
	return m
}

func (m *Multiverse) Context() context.Context {
	return m.ctx
}

func (m *Multiverse) Close() error {
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()
	for _, u := range m.universes {
		u.Cleanup()
	}
	m.universes = nil
	m.ctxC()
	return nil
}

func (m *Multiverse) Universe(name string) (*Universe, error) {
	name = strings.ToLower(name)
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()
	u, exists := m.universes[name]
	if !exists {
		return nil, fmt.Errorf("universe not found")
	}
	return u, nil
}

func (m *Multiverse) Delete(name string) error {
	name = strings.ToLower(name)
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()

	if _, exists := m.universes[name]; !exists {
		return fmt.Errorf("universe not found")
	}

	delete(m.universes, name)

	return nil
}

func (m *Multiverse) Status() Status {
	status := make(Status)
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()
	for _, u := range m.universes {
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
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()
	for _, u := range m.universes {
		u.lock.RLock()
		status[u.name] = UniverseStatus{
			SwarmKey:  u.swarmKey,
			NodeCount: len(u.all),
		}
		u.lock.RUnlock()
	}
	return status
}
