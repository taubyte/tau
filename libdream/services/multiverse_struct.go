package services

import (
	"context"

	"github.com/taubyte/tau/libdream/common"
	"github.com/taubyte/tau/libdream/registry"
)

type multiverse struct {
}

var _ common.Multiverse = &multiverse{}

func (m *multiverse) Context() context.Context {
	return multiverseCtx
}

func (m *multiverse) Exist(universe string) bool {
	return Exist(universe)
}

func (m *multiverse) Universe(name string) common.Universe {
	return Multiverse(UniverseConfig{Name: name})
}

type UniverseConfig struct {
	Name     string
	Id       string
	KeepRoot bool
}

func (m *multiverse) ValidServices() []string {
	return ValidServices()
}

func (m *multiverse) ValidFixtures() []string {
	return registry.ValidFixtures()
}

func (m *multiverse) ValidClients() []string {
	return ValidClients()
}

type serviceStatus struct {
	Name   string `json:"name"`
	Copies int    `json:"copies"`
}

func (m *multiverse) Status() interface{} {
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
