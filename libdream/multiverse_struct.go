package libdream

import (
	"context"
)

type Multiverse struct{}

func (m *Multiverse) Context() context.Context {
	return multiverseCtx
}

func (m *Multiverse) Universe(name string) *Universe {
	return NewUniverse(UniverseConfig{Name: name})
}

type UniverseConfig struct {
	Name     string
	Id       string
	KeepRoot bool
}

type serviceStatus struct {
	Name   string `json:"name"`
	Copies int    `json:"copies"`
}

func (m *Multiverse) Status() interface{} {
	status := make(map[string]interface{})
	for _, u := range universes {
		u.lock.RLock()
		status[u.name] = map[string]interface{}{
			"root":       u.root,
			"node-count": len(u.all),
			"simples": func() []string {
				_simples := make([]string, 0)
				for s := range u.simples {
					_simples = append(_simples, s)
				}
				return _simples
			}(),
			"nodes": func() map[string][]string {
				_nodes := make(map[string][]string)
				for _, s := range u.all {
					addrs := make([]string, 0)
					for _, addr := range s.Peer().Addrs() {
						addrs = append(addrs, addr.String())
					}
					_nodes[s.ID().Pretty()] = addrs
				}
				return _nodes
			}(),
			"services": func() []serviceStatus {
				_services := make([]serviceStatus, 0)
				for _, name := range ValidServices() {
					nodes := u.service[name].nodes
					if nodes != nil {
						_services = append(_services, serviceStatus{Name: name, Copies: len(nodes)})
					}
				}
				return _services
			}(),
		}
		u.lock.RUnlock()
	}
	return status
}
